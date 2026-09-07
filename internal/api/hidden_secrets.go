package api

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

type hiddenSecretsResponse struct {
	Secrets []models.HiddenSecret `json:"secrets"`
	pageMeta
}

// handleListHiddenSecrets lists the active hidden pictures with the caller's
// reveal progress.
//
// @Summary      List hidden secrets
// @Tags         hidden-secrets
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  hiddenSecretsResponse
// @Router       /api/v1/hidden-secrets [get]
func (s *Server) handleListHiddenSecrets(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleListHiddenSecrets: start userID=%s page=%d limit=%d", uid, page.Number, page.Limit)
	list, total, err := s.store.ListHiddenSecrets(r.Context(), uid, page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleListHiddenSecrets: error ListHiddenSecrets userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load hidden secrets")
		return
	}
	log.Printf("[DEBUG] handleListHiddenSecrets: success userID=%s count=%d total=%d", uid, len(list), total)
	httpx.JSON(w, http.StatusOK, hiddenSecretsResponse{Secrets: s.decorateHiddenSecrets(list), pageMeta: metaFor(page, total)})
}

// handleGetHiddenSecret returns one hidden picture's tile settings and the
// caller's revealed tiles.
//
// @Summary      Get hidden secret
// @Tags         hidden-secrets
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Hidden secret id (UUID)"
// @Success      200  {object}  models.HiddenSecretDetail
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/hidden-secrets/{id} [get]
func (s *Server) handleGetHiddenSecret(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleGetHiddenSecret: error invalid secret id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid hidden secret id")
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleGetHiddenSecret: start userID=%s secretID=%s", uid, id)

	d, err := s.store.GetHiddenSecret(r.Context(), id, uid)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleGetHiddenSecret: not found secretID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "hidden secret not found")
		return
	} else if err != nil {
		log.Printf("[DEBUG] handleGetHiddenSecret: error GetHiddenSecret secretID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load hidden secret")
		return
	}
	log.Printf("[DEBUG] handleGetHiddenSecret: success userID=%s secretID=%s", uid, id)
	httpx.JSON(w, http.StatusOK, d)
}

// hiddenSecretRevealResponse reports the updated reveal state and, once the
// ninth tile lands, the XP award and level progress — the same XP fields the
// dungeon/story completions return, so the client reuses its win-screen UI.
type hiddenSecretRevealResponse struct {
	Revealed  []int  `json:"revealed"`
	TileCount int    `json:"tileCount"`
	Completed bool   `json:"completed"`
	Status    string `json:"status"`

	XPEarned       int `json:"xpEarned"`
	XP             int `json:"xp"`
	Level          int `json:"level"`
	XPIntoLevel    int `json:"xpIntoLevel"`
	XPForNextLevel int `json:"xpForNextLevel"`

	// PointsEarned is the unlock points this reveal paid out (the whole picture
	// pays models.HiddenSecretPointsReward on the completing reveal, 0
	// otherwise), and UnlockPoints the resulting balance.
	PointsEarned int `json:"pointsEarned"`
	UnlockPoints int `json:"unlockPoints"`
}

// handleRevealHiddenSecretTile opens one shutter after its puzzle was solved.
// Revealing the ninth tile completes the picture: it awards the XP reward and
// unlocks the picture into the caller's gallery.
//
// @Summary      Reveal hidden secret tile
// @Tags         hidden-secrets
// @Produce      json
// @Security     BearerAuth
// @Param        id         path  string  true  "Hidden secret id (UUID)"
// @Param        tileIndex  path  int     true  "Tile index (0-8, row-major)"
// @Success      200  {object}  hiddenSecretRevealResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Router       /api/v1/hidden-secrets/{id}/tiles/{tileIndex}/reveal [post]
func (s *Server) handleRevealHiddenSecretTile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: error invalid secret id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid hidden secret id")
		return
	}
	tileIndex, err := strconv.Atoi(r.PathValue("tileIndex"))
	if err != nil {
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: error invalid tile index %q: %v", r.PathValue("tileIndex"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid tile index")
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleRevealHiddenSecretTile: start userID=%s secretID=%s tile=%d", uid, id, tileIndex)

	revealed, completed, awarded, err := s.store.RevealHiddenSecretTile(r.Context(), uid, id, tileIndex)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: not found secretID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "hidden secret not found")
		return
	case errors.Is(err, repository.ErrTileOutOfRange):
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: tile out of range tile=%d", tileIndex)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "tile index out of range")
		return
	case errors.Is(err, repository.ErrSecretInactive):
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: secret inactive secretID=%s", id)
		httpx.Error(w, http.StatusConflict, "inactive", "hidden secret is inactive")
		return
	case errors.Is(err, repository.ErrSecretCompleted):
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: already completed secretID=%s userID=%s", id, uid)
		httpx.Error(w, http.StatusConflict, "already_completed", "hidden secret already revealed")
		return
	case errors.Is(err, repository.ErrTileRevealed):
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: tile already revealed tile=%d", tileIndex)
		httpx.Error(w, http.StatusConflict, "already_revealed", "tile already revealed")
		return
	case err != nil:
		log.Printf("[DEBUG] handleRevealHiddenSecretTile: error RevealHiddenSecretTile userID=%s secretID=%s: %v", uid, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not reveal tile")
		return
	}

	resp := hiddenSecretRevealResponse{
		Revealed:  revealed,
		TileCount: models.HiddenSecretTileCount,
		Completed: completed,
	}
	if completed {
		resp.Status = models.StatusCompleted
	}

	// XP is only credited by the completing reveal; the other eight report the
	// caller's current totals so the client can still render an XP bar.
	total := 0
	if awarded > 0 {
		if t, e := s.store.AddXP(r.Context(), uid, awarded); e == nil {
			total = t
			resp.XPEarned = awarded
			log.Printf("[DEBUG] handleRevealHiddenSecretTile: XP awarded userID=%s secretID=%s xpEarned=%d totalXP=%d", uid, id, awarded, total)
		} else {
			log.Printf("[DEBUG] handleRevealHiddenSecretTile: error AddXP userID=%s: %v", uid, e)
		}
	} else {
		total, _ = s.store.GetXP(r.Context(), uid)
	}
	level, into, next := models.LevelFromXP(total)
	resp.XP = total
	resp.Level = level
	resp.XPIntoLevel = into
	resp.XPForNextLevel = next

	// Unlock points, likewise paid only by the completing reveal; the other eight
	// just report the caller's current balance. RevealHiddenSecretTile reports
	// completed=true only for the reveal that actually closed the board out (a
	// later call fails with ErrSecretCompleted), so that flag is what keeps this
	// grant from being farmable.
	if completed {
		if balance, perr := s.store.AddUnlockPoints(r.Context(), uid, models.HiddenSecretPointsReward); perr == nil {
			resp.PointsEarned = models.HiddenSecretPointsReward
			resp.UnlockPoints = balance
			log.Printf("[DEBUG] handleRevealHiddenSecretTile: unlock points awarded userID=%s secretID=%s pointsEarned=%d balance=%d",
				uid, id, models.HiddenSecretPointsReward, balance)
		} else {
			log.Printf("[DEBUG] handleRevealHiddenSecretTile: error AddUnlockPoints userID=%s: %v", uid, perr)
		}
	} else if balance, perr := s.store.GetUnlockPoints(r.Context(), uid); perr == nil {
		resp.UnlockPoints = balance
	}

	event := "tile_revealed"
	if completed {
		event = "complete"
	}
	metrics.GameEventsTotal.WithLabelValues("hidden_secret", event).Inc()
	log.Printf("[DEBUG] handleRevealHiddenSecretTile: success userID=%s secretID=%s tile=%d revealed=%d completed=%t",
		uid, id, tileIndex, len(revealed), completed)
	httpx.JSON(w, http.StatusOK, resp)
}

type hiddenSecretUnlocksResponse struct {
	Unlocks []models.HiddenSecretUnlock `json:"unlocks"`
}

// handleListHiddenSecretUnlocks lists the pictures the caller has fully
// revealed, newest first.
//
// @Summary      List unlocked hidden secrets
// @Tags         hidden-secrets
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  hiddenSecretUnlocksResponse
// @Router       /api/v1/hidden-secret-unlocks [get]
func (s *Server) handleListHiddenSecretUnlocks(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleListHiddenSecretUnlocks: start userID=%s", uid)
	list, err := s.store.ListUnlockedHiddenSecrets(r.Context(), uid)
	if err != nil {
		log.Printf("[DEBUG] handleListHiddenSecretUnlocks: error ListUnlockedHiddenSecrets userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load unlocked pictures")
		return
	}
	log.Printf("[DEBUG] handleListHiddenSecretUnlocks: success userID=%s count=%d", uid, len(list))
	httpx.JSON(w, http.StatusOK, hiddenSecretUnlocksResponse{Unlocks: list})
}
