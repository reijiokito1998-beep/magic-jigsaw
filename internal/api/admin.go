package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

// handleAdminCreateChallenge uploads a challenge image to Cloudinary and creates
// (or replaces) the daily challenge for the given date.
//
// @Summary      Create daily challenge (admin)
// @Description  Uploads a challenge image to Cloudinary and creates/replaces the daily challenge for a date.
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        file         formData  file    true   "Challenge image (max 15MB)"
// @Param        playSeconds  formData  int     true   "Time limit to complete, in seconds"
// @Param        gridCols     formData  int     false  "Grid columns (default 6)"
// @Param        gridRows     formData  int     false  "Grid rows (default 10)"
// @Param        category     formData  string  false  "Category (Nature/Space/Architecture/Art)"
// @Param        date         formData  string  false  "Challenge date YYYY-MM-DD (default: next free queue day)"
// @Param        title        formData  string  false  "Optional title"
// @Param        xpReward     formData  int     false  "XP awarded on completion (default 100)"
// @Param        challengeEventsEnabled formData bool false "Enable in-game disruption events (default false)"
// @Param        quizQuestion       formData  string  false  "Bonus question asked after solving (blank = no quiz)"
// @Param        quizOptionA        formData  string  false  "Answer option A (required when quizQuestion is set)"
// @Param        quizOptionB        formData  string  false  "Answer option B (required when quizQuestion is set)"
// @Param        quizCorrectOption  formData  int     false  "Which option is correct: 0 = A, 1 = B"
// @Success      201  {object}  models.AdminChallengeView
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/challenges [post]
func (s *Server) handleAdminCreateChallenge(w http.ResponseWriter, r *http.Request) {
	adminID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAdminCreateChallenge: start adminID=%s", adminID)
	// Image from the library (imageId) or an uploaded file; parses the form.
	imageID, ok := s.resolveImageID(w, r, true)
	if !ok {
		log.Printf("[DEBUG] handleAdminCreateChallenge: resolveImageID failed adminID=%s", adminID)
		return
	}

	date := r.FormValue("date")
	if date == "" {
		// No date → append to the next free queue slot.
		next, err := s.store.NextChallengeDate(r.Context())
		if err != nil {
			log.Printf("[DEBUG] handleAdminCreateChallenge: error NextChallengeDate: %v", err)
			httpx.Error(w, http.StatusInternalServerError, "internal", "could not pick a date")
			return
		}
		date = next
		log.Printf("[DEBUG] handleAdminCreateChallenge: no date provided, using next free date=%s", date)
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		log.Printf("[DEBUG] handleAdminCreateChallenge: error invalid date %q: %v", date, err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "date must be YYYY-MM-DD")
		return
	}

	playSeconds, err := strconv.Atoi(r.FormValue("playSeconds"))
	if err != nil || playSeconds <= 0 {
		log.Printf("[DEBUG] handleAdminCreateChallenge: error invalid playSeconds %q: %v", r.FormValue("playSeconds"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "playSeconds must be a positive integer")
		return
	}

	gridCols := atoiDefault(r.FormValue("gridCols"), 6)
	gridRows := atoiDefault(r.FormValue("gridRows"), 10)
	if gridCols <= 0 || gridRows <= 0 {
		log.Printf("[DEBUG] handleAdminCreateChallenge: error invalid grid gridCols=%d gridRows=%d", gridCols, gridRows)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "gridCols/gridRows must be positive")
		return
	}

	quiz, ok := parseQuizParams(w, r)
	if !ok {
		log.Printf("[DEBUG] handleAdminCreateChallenge: invalid quiz fields adminID=%s", adminID)
		return
	}

	createdBy := uuid.NullUUID{}
	if uid := httpx.UserIDFrom(r.Context()); uid != uuid.Nil {
		createdBy = uuid.NullUUID{UUID: uid, Valid: true}
	}

	challenge, err := s.store.CreateChallenge(r.Context(), repository.CreateChallengeParams{
		Date:                   date,
		ImageID:                imageID,
		PlaySeconds:            playSeconds,
		GridCols:               gridCols,
		GridRows:               gridRows,
		Category:               r.FormValue("category"),
		Title:                  r.FormValue("title"),
		XPReward:               atoiDefault(r.FormValue("xpReward"), 100),
		ChallengeEventsEnabled: boolDefault(r.FormValue("challengeEventsEnabled"), false),
		Quiz:                   quiz,
		CreatedBy:              createdBy,
	})
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateChallenge: error CreateChallenge date=%s: %v", date, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create challenge")
		return
	}
	log.Printf("[DEBUG] handleAdminCreateChallenge: success challengeID=%s date=%s", challenge.ID, date)
	httpx.JSON(w, http.StatusCreated, challenge.AdminView())
}

// parseQuizParams reads the bonus-question fields off an admin form. A blank
// question clears the quiz; a question without both options is rejected rather
// than saved half-configured, which would show players an unanswerable card.
func parseQuizParams(w http.ResponseWriter, r *http.Request) (repository.QuizParams, bool) {
	question := strings.TrimSpace(r.FormValue("quizQuestion"))
	if question == "" {
		return repository.QuizParams{}, true
	}
	optionA := strings.TrimSpace(r.FormValue("quizOptionA"))
	optionB := strings.TrimSpace(r.FormValue("quizOptionB"))
	if optionA == "" || optionB == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request",
			"quizOptionA and quizOptionB are required when quizQuestion is set")
		return repository.QuizParams{}, false
	}
	correct := atoiDefault(r.FormValue("quizCorrectOption"), 0)
	if correct != 0 && correct != 1 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "quizCorrectOption must be 0 or 1")
		return repository.QuizParams{}, false
	}
	return repository.QuizParams{
		Question:      question,
		OptionA:       optionA,
		OptionB:       optionB,
		CorrectOption: correct,
	}, true
}

// handleAdminListChallenges lists recent challenges for the admin.
//
// @Summary      List challenges (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  adminChallengesResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/challenges [get]
func (s *Server) handleAdminListChallenges(w http.ResponseWriter, r *http.Request) {
	// The queue has always been capped at 100; an absent limit keeps that.
	page := httpx.ParsePage(r, maxPageLimit)
	limit := page.Limit
	if limit == 0 {
		limit = maxPageLimit
	}
	log.Printf("[DEBUG] handleAdminListChallenges: start adminID=%s page=%d limit=%d",
		httpx.UserIDFrom(r.Context()), page.Number, limit)
	challenges, total, err := s.store.ListChallenges(r.Context(), limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleAdminListChallenges: error ListChallenges: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load challenges")
		return
	}
	// Managers edit the quiz, so they get the answer key players never see.
	views := make([]models.AdminChallengeView, 0, len(challenges))
	for _, c := range challenges {
		views = append(views, c.AdminView())
	}
	log.Printf("[DEBUG] handleAdminListChallenges: success count=%d total=%d", len(views), total)
	httpx.JSON(w, http.StatusOK, adminChallengesResponse{Challenges: s.decorateAdminChallenges(views), pageMeta: metaFor(page, total)})
}

// handleAdminUpdateChallenge edits an existing challenge. The image is optional
// (keeps the current one when no file is uploaded); the date is unchanged.
//
// @Summary      Update daily challenge (admin)
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id           path      string  true   "Challenge id (UUID)"
// @Param        file         formData  file    false  "New image (optional)"
// @Param        playSeconds  formData  int     true   "Time limit in seconds"
// @Param        gridCols     formData  int     false  "Grid columns"
// @Param        gridRows     formData  int     false  "Grid rows"
// @Param        category     formData  string  false  "Category"
// @Param        title        formData  string  false  "Title"
// @Param        xpReward     formData  int     false  "XP awarded on completion (default 100)"
// @Param        challengeEventsEnabled formData bool false "Enable in-game disruption events (default false)"
// @Param        quizQuestion       formData  string  false  "Bonus question asked after solving (blank clears the quiz)"
// @Param        quizOptionA        formData  string  false  "Answer option A (required when quizQuestion is set)"
// @Param        quizOptionB        formData  string  false  "Answer option B (required when quizQuestion is set)"
// @Param        quizCorrectOption  formData  int     false  "Which option is correct: 0 = A, 1 = B"
// @Success      200  {object}  models.AdminChallengeView
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/challenges/{id} [put]
func (s *Server) handleAdminUpdateChallenge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateChallenge: error invalid challenge id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid challenge id")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateChallenge: start challengeID=%s adminID=%s", id, httpx.UserIDFrom(r.Context()))
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		log.Printf("[DEBUG] handleAdminUpdateChallenge: error ParseMultipartForm: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid multipart form")
		return
	}

	playSeconds, err := strconv.Atoi(r.FormValue("playSeconds"))
	if err != nil || playSeconds <= 0 {
		log.Printf("[DEBUG] handleAdminUpdateChallenge: error invalid playSeconds %q: %v", r.FormValue("playSeconds"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "playSeconds must be a positive integer")
		return
	}
	gridCols := atoiDefault(r.FormValue("gridCols"), 6)
	gridRows := atoiDefault(r.FormValue("gridRows"), 10)
	if gridCols <= 0 || gridRows <= 0 {
		log.Printf("[DEBUG] handleAdminUpdateChallenge: error invalid grid gridCols=%d gridRows=%d", gridCols, gridRows)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "gridCols/gridRows must be positive")
		return
	}

	// Optional new image (library imageId or uploaded file).
	imageID, ok := s.optionalImageID(w, r, true)
	if !ok {
		log.Printf("[DEBUG] handleAdminUpdateChallenge: optionalImageID failed challengeID=%s", id)
		return
	}

	// Like the events flag: the client always sends these, so a blank question
	// on an edit means "remove the quiz" rather than "leave it alone".
	quiz, ok := parseQuizParams(w, r)
	if !ok {
		log.Printf("[DEBUG] handleAdminUpdateChallenge: invalid quiz fields challengeID=%s", id)
		return
	}

	challenge, err := s.store.UpdateChallenge(r.Context(), repository.UpdateChallengeParams{
		ID:          id,
		Title:       r.FormValue("title"),
		Category:    r.FormValue("category"),
		PlaySeconds: playSeconds,
		GridCols:    gridCols,
		GridRows:    gridRows,
		XPReward:    atoiDefault(r.FormValue("xpReward"), 100),
		// Absent field reads as "off" so an edit can turn the events back off;
		// the client always sends it explicitly.
		ChallengeEventsEnabled: boolDefault(r.FormValue("challengeEventsEnabled"), false),
		Quiz:                   quiz,
		ImageID:                imageID,
	})
	if err != nil {
		if err == repository.ErrNotFound {
			log.Printf("[DEBUG] handleAdminUpdateChallenge: challenge not found challengeID=%s", id)
			httpx.Error(w, http.StatusNotFound, "not_found", "challenge not found")
			return
		}
		log.Printf("[DEBUG] handleAdminUpdateChallenge: error UpdateChallenge challengeID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update challenge")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateChallenge: success challengeID=%s", id)
	httpx.JSON(w, http.StatusOK, challenge.AdminView())
}

// boolDefault parses s as a bool ("true"/"false"/"1"/"0"), returning def on
// empty/invalid input.
func boolDefault(s string, def bool) bool {
	if s == "" {
		return def
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return b
}

// atoiDefault parses s as an int, returning def on empty/invalid input.
// boolForm reads a checkbox-style multipart field. Accepts what the various
// clients actually send ("true"/"1"/"on"/"yes"); anything else, including an
// absent field, is false.
func boolForm(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
