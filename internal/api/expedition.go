package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/models"
)

type expeditionFinishRequest struct {
	ElapsedSeconds int  `json:"elapsedSeconds"`
	CorrectPieces  *int `json:"correctPieces"`
}

// buildExpeditionState assembles the personalized campaign for a user.
func (s *Server) buildExpeditionState(w http.ResponseWriter, r *http.Request) (*models.ExpeditionState, bool) {
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)
	log.Printf("[DEBUG] buildExpeditionState: start userID=%s", uid)

	images, err := s.store.LevelImages(ctx)
	if err != nil {
		log.Printf("[DEBUG] buildExpeditionState: error LevelImages: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load levels")
		return nil, false
	}
	progress, err := s.store.GetExpeditionProgress(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] buildExpeditionState: error GetExpeditionProgress userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load progress")
		return nil, false
	}
	totalStars, err := s.store.TotalExpeditionStars(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] buildExpeditionState: error TotalExpeditionStars userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not compute stars")
		return nil, false
	}
	tiers, err := s.store.EffectiveTiers(ctx)
	if err != nil {
		log.Printf("[DEBUG] buildExpeditionState: error EffectiveTiers: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load level config")
		return nil, false
	}
	levelCount, err := s.store.ExpeditionLevelCount(ctx)
	if err != nil {
		log.Printf("[DEBUG] buildExpeditionState: error ExpeditionLevelCount: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load levels")
		return nil, false
	}

	levels := make([]models.ExpeditionLevel, 0, levelCount)
	completed := 0
	// A level requires the cumulative stars of everything before it. Walking
	// the levels in order lets that be a running sum instead of re-summing the
	// prefix per level, which matters now that the campaign can grow.
	req := 0
	for i := 1; i <= levelCount; i++ {
		tier := tiers[i]
		// A collection level is always open and costs nothing to reach, so it
		// neither reads the running total nor adds to it — the map's star
		// ladder steps straight over it.
		unlocked := !tier.ShowOnMain || totalStars >= req
		requiredStars := req
		if !tier.ShowOnMain {
			requiredStars = 0
		}

		status := "locked"
		starsEarned := 0
		if p, ok := progress[i]; ok {
			status = p.Status
			if p.Status == "completed" {
				starsEarned = tier.Star
				// Only map levels move the campaign counters; a cleared
				// collection level must not advance "level 7 of 100".
				if tier.ShowOnMain {
					completed++
				}
			}
		} else if unlocked {
			status = "available"
		}
		if !unlocked {
			status = "locked"
		}

		levels = append(levels, models.ExpeditionLevel{
			Index:         i,
			Star:          tier.Star,
			GridCols:      tier.GridCols,
			GridRows:      tier.GridRows,
			PlaySeconds:   tier.PlaySeconds,
			Category:      tier.Category,
			RequiredStars: requiredStars,
			ImageURL:      images[i].URL,
			ImageID:       images[i].ID,
			Status:        status,
			Unlocked:      unlocked,
			StarsEarned:   starsEarned,
			XPReward:      tier.XPReward,
			ShowOnMain:    tier.ShowOnMain,
		})
		if tier.ShowOnMain {
			req += tier.Star
		}
	}

	// Capped against the map's length, not the row count — collection levels
	// are not part of the campaign the player is progressing through.
	mainCount := 0
	for i := 1; i <= levelCount; i++ {
		if tiers[i].ShowOnMain {
			mainCount++
		}
	}
	current := completed + 1
	if current > mainCount {
		current = mainCount
	}

	log.Printf("[DEBUG] buildExpeditionState: success userID=%s totalStars=%d completed=%d currentLevel=%d", uid, totalStars, completed, current)
	return &models.ExpeditionState{
		TotalStars:     totalStars,
		CompletedCount: completed,
		CurrentLevel:   current,
		Levels:         levels,
	}, true
}

// handleExpedition returns the full expedition campaign for the user.
//
// @Summary      Expedition campaign
// @Description  100-level campaign with per-user progress, stars and unlock state.
// @Tags         expedition
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  models.ExpeditionState
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/expedition [get]
func (s *Server) handleExpedition(w http.ResponseWriter, r *http.Request) {
	log.Printf("[DEBUG] handleExpedition: start userID=%s", httpx.UserIDFrom(r.Context()))
	state, ok := s.buildExpeditionState(w, r)
	if !ok {
		return
	}
	// The star-gated unlock chain is a running total over the whole campaign,
	// so the state is always computed in full and only the returned window is
	// cut — totalStars, completedCount and currentLevel stay campaign-wide.
	page := httpx.ParsePage(r, maxPageLimit)
	total := len(state.Levels)
	state.Levels = s.decorateExpeditionLevels(pageOf(state.Levels, page))
	log.Printf("[DEBUG] handleExpedition: success levels=%d of %d", len(state.Levels), total)
	httpx.JSON(w, http.StatusOK, expeditionResponse{ExpeditionState: state, pageMeta: metaFor(page, total)})
}

// expeditionResponse is the campaign state plus the window its levels came from.
type expeditionResponse struct {
	*models.ExpeditionState
	pageMeta
}

// pageOf slices an already-built list to the requested window. Used where the
// rows cannot be paginated in SQL because building any one of them depends on
// all the ones before it.
func pageOf[T any](all []T, p httpx.Page) []T {
	if !p.Requested() || p.Offset >= len(all) {
		if p.Requested() {
			return all[:0]
		}
		return all
	}
	end := p.Offset + p.Limit
	if end > len(all) {
		end = len(all)
	}
	return all[p.Offset:end]
}

// levelFromPath parses the {level} path value and bounds it to the campaign as
// it exists right now (1..N), which grows as an admin adds levels.
func (s *Server) levelFromPath(w http.ResponseWriter, r *http.Request) (int, bool) {
	level, err := strconv.Atoi(r.PathValue("level"))
	if err != nil || level < 1 {
		log.Printf("[DEBUG] levelFromPath: error invalid level %q: %v", r.PathValue("level"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid level")
		return 0, false
	}
	count, err := s.store.ExpeditionLevelCount(r.Context())
	if err != nil {
		log.Printf("[DEBUG] levelFromPath: error ExpeditionLevelCount: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load levels")
		return 0, false
	}
	if level > count {
		log.Printf("[DEBUG] levelFromPath: level %d out of range (count=%d)", level, count)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid level")
		return 0, false
	}
	return level, true
}

// handleStartExpedition starts a level (must be unlocked and have an image).
//
// @Summary      Start expedition level
// @Tags         expedition
// @Produce      json
// @Security     BearerAuth
// @Param        level  path  int  true  "Level index (1-100)"
// @Success      200  {object}  models.ExpeditionState
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Router       /api/v1/expedition/{level}/start [post]
func (s *Server) handleStartExpedition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)
	level, ok := s.levelFromPath(w, r)
	if !ok {
		return
	}
	log.Printf("[DEBUG] handleStartExpedition: start userID=%s level=%d", uid, level)

	totalStars, err := s.store.TotalExpeditionStars(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] handleStartExpedition: error TotalExpeditionStars userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not compute stars")
		return
	}
	tiers, err := s.store.EffectiveTiers(ctx)
	if err != nil {
		log.Printf("[DEBUG] handleStartExpedition: error EffectiveTiers: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load level config")
		return
	}
	// Collection levels are free to play — the whole point of the flag — so the
	// star gate is skipped for them. Enforced here and not only in the client:
	// the app can filter a level out of the map, but only the server can stop a
	// crafted request from starting a level the player has not paid for.
	if tiers[level].ShowOnMain && totalStars < models.RequiredStarsFromTiers(tiers, level) {
		log.Printf("[DEBUG] handleStartExpedition: level locked userID=%s level=%d totalStars=%d", uid, level, totalStars)
		httpx.Error(w, http.StatusForbidden, "locked", "not enough stars to unlock this level")
		return
	}
	if _, has, _ := s.store.LevelImageID(ctx, level); !has {
		log.Printf("[DEBUG] handleStartExpedition: level image not available level=%d", level)
		httpx.Error(w, http.StatusForbidden, "no_image", "level image not available yet")
		return
	}
	if err := s.store.StartExpeditionLevel(ctx, uid, level); err != nil {
		log.Printf("[DEBUG] handleStartExpedition: error StartExpeditionLevel userID=%s level=%d: %v", uid, level, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not start level")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("expedition", "start").Inc()
	log.Printf("[DEBUG] handleStartExpedition: success userID=%s level=%d", uid, level)
	state, ok := s.buildExpeditionState(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

// handleCompleteExpedition marks a level completed and awards its stars (once).
//
// @Summary      Complete expedition level
// @Tags         expedition
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        level    path  int                       true   "Level index (1-100)"
// @Param        request  body  expeditionFinishRequest   false  "Elapsed + correct pieces"
// @Success      200  {object}  expeditionCompleteResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/expedition/{level}/complete [post]
func (s *Server) handleCompleteExpedition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)
	level, ok := s.levelFromPath(w, r)
	if !ok {
		return
	}
	log.Printf("[DEBUG] handleCompleteExpedition: start userID=%s level=%d", uid, level)
	totalStars, err := s.store.TotalExpeditionStars(ctx, uid)
	if err != nil {
		log.Printf("[DEBUG] handleCompleteExpedition: error TotalExpeditionStars userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not compute stars")
		return
	}
	tiers, err := s.store.EffectiveTiers(ctx)
	if err != nil {
		log.Printf("[DEBUG] handleCompleteExpedition: error EffectiveTiers: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load level config")
		return
	}
	// Same exemption as the start gate — see there.
	if tiers[level].ShowOnMain && totalStars < models.RequiredStarsFromTiers(tiers, level) {
		log.Printf("[DEBUG] handleCompleteExpedition: level locked userID=%s level=%d totalStars=%d", uid, level, totalStars)
		httpx.Error(w, http.StatusForbidden, "locked", "level not unlocked")
		return
	}
	var req expeditionFinishRequest
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleCompleteExpedition: error decoding request body userID=%s level=%d", uid, level)
		return
	}
	tier := tiers[level]
	correct := tier.Pieces()
	if req.CorrectPieces != nil && *req.CorrectPieces >= 0 && *req.CorrectPieces < correct {
		correct = *req.CorrectPieces
	}
	first, err := s.store.CompleteExpeditionLevel(ctx, uid, level, req.ElapsedSeconds, correct)
	if err != nil {
		log.Printf("[DEBUG] handleCompleteExpedition: error CompleteExpeditionLevel userID=%s level=%d: %v", uid, level, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not complete level")
		return
	}

	// Award the tier's XP only on the first completion of this level.
	var resp expeditionCompleteResponse
	total := 0
	if first && tier.XPReward > 0 {
		if t, e := s.store.AddXP(ctx, uid, tier.XPReward); e == nil {
			total = t
			resp.XPEarned = tier.XPReward
			log.Printf("[DEBUG] handleCompleteExpedition: XP awarded userID=%s level=%d xpEarned=%d totalXP=%d", uid, level, tier.XPReward, total)
		} else {
			log.Printf("[DEBUG] handleCompleteExpedition: error AddXP userID=%s: %v", uid, e)
		}
	} else {
		total, _ = s.store.GetXP(ctx, uid)
	}
	lvl, into, next := models.LevelFromXP(total)
	resp.XP = total
	resp.Level = lvl
	resp.XPIntoLevel = into
	resp.XPForNextLevel = next

	state, ok := s.buildExpeditionState(w, r)
	if !ok {
		return
	}
	resp.ExpeditionState = *state
	metrics.GameEventsTotal.WithLabelValues("expedition", "complete").Inc()
	log.Printf("[DEBUG] handleCompleteExpedition: success userID=%s level=%d first=%t", uid, level, first)
	httpx.JSON(w, http.StatusOK, resp)
}

// expeditionCompleteResponse is the campaign state plus XP awarded and the
// user's resulting level progress (for the win screen).
type expeditionCompleteResponse struct {
	models.ExpeditionState
	XPEarned       int `json:"xpEarned"`
	XP             int `json:"xp"`
	Level          int `json:"level"`
	XPIntoLevel    int `json:"xpIntoLevel"`
	XPForNextLevel int `json:"xpForNextLevel"`
}

// handleFailExpedition marks an in-progress level failed (retryable).
//
// @Summary      Fail expedition level
// @Tags         expedition
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        level    path  int                       true   "Level index (1-100)"
// @Param        request  body  expeditionFinishRequest   false  "Correct pieces so far"
// @Success      200  {object}  models.ExpeditionState
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/expedition/{level}/fail [post]
func (s *Server) handleFailExpedition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)
	level, ok := s.levelFromPath(w, r)
	if !ok {
		return
	}
	log.Printf("[DEBUG] handleFailExpedition: start userID=%s level=%d", uid, level)
	var req expeditionFinishRequest
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleFailExpedition: error decoding request body userID=%s level=%d", uid, level)
		return
	}
	correct := 0
	if req.CorrectPieces != nil && *req.CorrectPieces > 0 {
		correct = *req.CorrectPieces
	}
	if err := s.store.FailExpeditionLevel(ctx, uid, level, correct); err != nil {
		log.Printf("[DEBUG] handleFailExpedition: error FailExpeditionLevel userID=%s level=%d: %v", uid, level, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update level")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("expedition", "fail").Inc()
	log.Printf("[DEBUG] handleFailExpedition: success userID=%s level=%d correctPieces=%d", uid, level, correct)
	state, ok := s.buildExpeditionState(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

// --- Admin ------------------------------------------------------------------

type adminExpeditionLevel struct {
	Index       int    `json:"index"`
	Star        int    `json:"star"`
	GridCols    int    `json:"gridCols"`
	GridRows    int    `json:"gridRows"`
	PlaySeconds int    `json:"playSeconds"`
	Category    string `json:"category"`
	XPReward    int    `json:"xpReward"`
	// ShowOnMain false hides the level from the campaign map and puts it in
	// the collection named by Category instead.
	ShowOnMain bool   `json:"showOnMain"`
	ImageURL   string `json:"imageUrl"`
	// ThumbURL is [ImageURL] sized for the admin grid tile.
	ThumbURL string `json:"thumbUrl,omitempty"`
	HasImage bool   `json:"hasImage"`
}

// adminLevelView builds the admin row for a level from its effective tier.
func (s *Server) adminLevelView(index int, t models.ExpeditionTier, imageURL string) adminExpeditionLevel {
	return adminExpeditionLevel{
		Index:       index,
		Star:        t.Star,
		GridCols:    t.GridCols,
		GridRows:    t.GridRows,
		PlaySeconds: t.PlaySeconds,
		Category:    t.Category,
		XPReward:    t.XPReward,
		ShowOnMain:  t.ShowOnMain,
		ImageURL:    imageURL,
		ThumbURL:    s.thumb(imageURL),
		HasImage:    imageURL != "",
	}
}

type adminExpeditionResponse struct {
	Levels []adminExpeditionLevel `json:"levels"`
	pageMeta
}

// handleAdminListExpedition lists every level with its image state.
//
// @Summary      List expedition levels (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole campaign"
// @Success      200  {object}  adminExpeditionResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/expedition [get]
func (s *Server) handleAdminListExpedition(w http.ResponseWriter, r *http.Request) {
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleAdminListExpedition: start adminID=%s page=%d limit=%d",
		httpx.UserIDFrom(r.Context()), page.Number, page.Limit)
	images, err := s.store.LevelImages(r.Context())
	if err != nil {
		log.Printf("[DEBUG] handleAdminListExpedition: error LevelImages: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load levels")
		return
	}
	tiers, err := s.store.EffectiveTiers(r.Context())
	if err != nil {
		log.Printf("[DEBUG] handleAdminListExpedition: error EffectiveTiers: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load level config")
		return
	}
	count, err := s.store.ExpeditionLevelCount(r.Context())
	if err != nil {
		log.Printf("[DEBUG] handleAdminListExpedition: error ExpeditionLevelCount: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load levels")
		return
	}
	// Levels are numbered 1..count, so the window is arithmetic — no need to
	// build rows the caller did not ask for.
	from, to := 1, count
	if page.Requested() {
		from = page.Offset + 1
		to = page.Offset + page.Limit
		if to > count {
			to = count
		}
	}
	levels := make([]adminExpeditionLevel, 0, to-from+1)
	for i := from; i <= to; i++ {
		levels = append(levels, s.adminLevelView(i, tiers[i], images[i].URL))
	}
	log.Printf("[DEBUG] handleAdminListExpedition: success count=%d total=%d", len(levels), count)
	httpx.JSON(w, http.StatusOK, adminExpeditionResponse{Levels: levels, pageMeta: metaFor(page, count)})
}

// maxExpeditionLevels caps how far the campaign can grow. Every level costs a
// row in the admin grid and a slot in the star-gated unlock chain, so an
// accidental key-repeat on "add level" should hit a wall rather than create
// thousands of empty levels.
const maxExpeditionLevels = 1000

// handleAdminCreateExpeditionLevel appends a new level at the end of the
// campaign. The level starts from the top-tier defaults with no image, so it
// stays unplayable until the admin assigns one.
//
// @Summary      Create an expedition level (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      201  {object}  adminExpeditionLevel
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/expedition [post]
func (s *Server) handleAdminCreateExpeditionLevel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.Printf("[DEBUG] handleAdminCreateExpeditionLevel: start adminID=%s", httpx.UserIDFrom(ctx))
	count, err := s.store.ExpeditionLevelCount(ctx)
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateExpeditionLevel: error ExpeditionLevelCount: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load levels")
		return
	}
	if count >= maxExpeditionLevels {
		log.Printf("[DEBUG] handleAdminCreateExpeditionLevel: level cap reached count=%d", count)
		httpx.Error(w, http.StatusConflict, "level_limit",
			"the expedition cannot have more levels")
		return
	}

	index, err := s.store.CreateExpeditionLevel(ctx)
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateExpeditionLevel: error CreateExpeditionLevel: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create level")
		return
	}
	log.Printf("[DEBUG] handleAdminCreateExpeditionLevel: success level=%d", index)
	httpx.JSON(w, http.StatusCreated, s.adminLevelView(index, models.TierForLevel(index), ""))
}

// handleAdminDeleteExpeditionLevel removes the last level, so an admin can undo
// an accidental "add level". Only the last one, and only while nobody has
// played it (see repository.DeleteLastExpeditionLevel).
//
// @Summary      Delete the last expedition level (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        level  path  int  true  "Level index (must be the last level)"
// @Success      204  "deleted"
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/expedition/{level} [delete]
func (s *Server) handleAdminDeleteExpeditionLevel(w http.ResponseWriter, r *http.Request) {
	level, ok := s.levelFromPath(w, r)
	if !ok {
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteExpeditionLevel: start adminID=%s level=%d",
		httpx.UserIDFrom(r.Context()), level)
	if level == 1 {
		// An empty campaign would break every "level 1 is always available"
		// assumption downstream, so keep at least one level.
		httpx.Error(w, http.StatusConflict, "level_required", "the expedition must keep at least one level")
		return
	}
	deleted, reason, err := s.store.DeleteLastExpeditionLevel(r.Context(), level)
	if err != nil {
		log.Printf("[DEBUG] handleAdminDeleteExpeditionLevel: error DeleteLastExpeditionLevel level=%d: %v", level, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete level")
		return
	}
	if !deleted {
		log.Printf("[DEBUG] handleAdminDeleteExpeditionLevel: refused level=%d reason=%s", level, reason)
		httpx.Error(w, http.StatusConflict, "delete_refused", reason)
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteExpeditionLevel: success level=%d", level)
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminUploadExpedition uploads/replaces the image of a level (Cloudinary).
//
// @Summary      Upload expedition level image (admin)
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        level  path      int   true  "Level index (1-100)"
// @Param        file   formData  file  true  "Image file (max 15MB)"
// @Success      200  {object}  adminExpeditionLevel
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/expedition/{level} [post]
func (s *Server) handleAdminUploadExpedition(w http.ResponseWriter, r *http.Request) {
	level, ok := s.levelFromPath(w, r)
	if !ok {
		return
	}
	adminID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAdminUploadExpedition: start adminID=%s level=%d", adminID, level)
	imageID, ok := s.resolveImageID(w, r, true)
	if !ok {
		log.Printf("[DEBUG] handleAdminUploadExpedition: resolveImageID failed level=%d", level)
		return
	}
	if err := s.store.SetLevelImage(r.Context(), level, imageID); err != nil {
		log.Printf("[DEBUG] handleAdminUploadExpedition: error SetLevelImage level=%d imageID=%s: %v", level, imageID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not set level image")
		return
	}
	img, _ := s.store.GetImage(r.Context(), imageID)
	cfgs, _ := s.store.LevelConfigs(r.Context())
	tier := models.EffectiveTier(level, cfgs[level])
	log.Printf("[DEBUG] handleAdminUploadExpedition: success level=%d imageID=%s", level, imageID)
	httpx.JSON(w, http.StatusOK, s.adminLevelView(level, tier, img.URL))
}

// adminExpeditionConfigRequest is the editable game config for a level. All
// fields are required so the admin form always sends a complete config.
type adminExpeditionConfigRequest struct {
	Star        int    `json:"star"`
	GridCols    int    `json:"gridCols"`
	GridRows    int    `json:"gridRows"`
	PlaySeconds int    `json:"playSeconds"`
	Category    string `json:"category"`
	XPReward    int    `json:"xpReward"`

	// ShowOnMain is a pointer so an older admin build, which does not send the
	// field, leaves the level on the map instead of silently hiding it.
	ShowOnMain *bool `json:"showOnMain"`
}

// handleAdminUpdateExpeditionConfig updates a level's game config (star, grid,
// time, category, XP). The level image is managed separately.
//
// @Summary      Update expedition level config (admin)
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        level    path  int                            true  "Level index (1-100)"
// @Param        request  body  adminExpeditionConfigRequest   true  "Level game config"
// @Success      200  {object}  adminExpeditionLevel
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/expedition/{level}/config [put]
func (s *Server) handleAdminUpdateExpeditionConfig(w http.ResponseWriter, r *http.Request) {
	level, ok := s.levelFromPath(w, r)
	if !ok {
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: start adminID=%s level=%d", httpx.UserIDFrom(r.Context()), level)
	var req adminExpeditionConfigRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: error decoding request body level=%d", level)
		return
	}
	// Validate ranges to keep the campaign playable.
	if req.Star < 1 || req.Star > 5 {
		log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: error star out of range star=%d", req.Star)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "star must be 1-5")
		return
	}
	if req.GridCols < 2 || req.GridCols > 20 || req.GridRows < 2 || req.GridRows > 20 {
		log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: error grid out of range gridCols=%d gridRows=%d", req.GridCols, req.GridRows)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "grid cols/rows must be 2-20")
		return
	}
	if req.PlaySeconds < 30 || req.PlaySeconds > 3600 {
		log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: error playSeconds out of range playSeconds=%d", req.PlaySeconds)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "playSeconds must be 30-3600")
		return
	}
	if req.XPReward < 0 || req.XPReward > 100000 {
		log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: error xpReward out of range xpReward=%d", req.XPReward)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "xpReward must be 0-100000")
		return
	}
	category := strings.TrimSpace(req.Category)
	if category == "" || len(category) > 80 {
		log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: error category invalid length=%d", len(category))
		httpx.Error(w, http.StatusBadRequest, "bad_request", "category must be 1-80 chars")
		return
	}

	showOnMain := true
	if req.ShowOnMain != nil {
		showOnMain = *req.ShowOnMain
	}
	cfg := models.LevelConfig{
		Star:        &req.Star,
		GridCols:    &req.GridCols,
		GridRows:    &req.GridRows,
		PlaySeconds: &req.PlaySeconds,
		Category:    &category,
		XPReward:    &req.XPReward,
		ShowOnMain:  &showOnMain,
	}
	if err := s.store.UpdateLevelConfig(r.Context(), level, cfg); err != nil {
		log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: error UpdateLevelConfig level=%d: %v", level, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update level config")
		return
	}

	images, _ := s.store.LevelImages(r.Context())
	tier := models.EffectiveTier(level, &cfg)
	log.Printf("[DEBUG] handleAdminUpdateExpeditionConfig: success level=%d", level)
	httpx.JSON(w, http.StatusOK, s.adminLevelView(level, tier, images[level].URL))
}
