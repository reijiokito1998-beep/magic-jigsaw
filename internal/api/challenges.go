package api

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

// handleTodayChallenge returns today's challenge with the user's result state.
//
// @Summary      Today's challenge
// @Description  Returns today's daily challenge along with the user's result (if any).
// @Tags         challenges
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  models.ChallengeView
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/challenges/today [get]
func (s *Server) handleTodayChallenge(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	today := time.Now().UTC().Format("2006-01-02")
	log.Printf("[DEBUG] handleTodayChallenge: start userID=%s date=%s", uid, today)
	challenge, err := s.store.GetChallengeByDate(r.Context(), today)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleTodayChallenge: no challenge for today date=%s", today)
		httpx.Error(w, http.StatusNotFound, "not_found", "no challenge for today")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleTodayChallenge: error GetChallengeByDate date=%s: %v", today, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load challenge")
		return
	}
	view, err := s.buildChallengeView(r.Context(), challenge)
	if err != nil {
		log.Printf("[DEBUG] handleTodayChallenge: error buildChallengeView challengeID=%s: %v", challenge.ID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load result")
		return
	}
	log.Printf("[DEBUG] handleTodayChallenge: success userID=%s challengeID=%s", uid, challenge.ID)
	httpx.JSON(w, http.StatusOK, view)
}

// handleListChallenges returns recent challenges (history).
//
// @Summary      List recent challenges
// @Tags         challenges
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  challengesResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Router       /api/v1/challenges [get]
func (s *Server) handleListChallenges(w http.ResponseWriter, r *http.Request) {
	// Historically capped at 30; an absent limit keeps that window.
	page := httpx.ParsePage(r, maxPageLimit)
	limit := page.Limit
	if limit == 0 {
		limit = defaultChallengeListLimit
	}
	log.Printf("[DEBUG] handleListChallenges: start userID=%s page=%d limit=%d",
		httpx.UserIDFrom(r.Context()), page.Number, limit)
	challenges, total, err := s.store.ListChallenges(r.Context(), limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleListChallenges: error ListChallenges: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load challenges")
		return
	}
	log.Printf("[DEBUG] handleListChallenges: success count=%d total=%d", len(challenges), total)
	httpx.JSON(w, http.StatusOK, challengesResponse{Challenges: s.decorateChallenges(challenges), pageMeta: metaFor(page, total)})
}

// challengeIDFromPath parses and loads the challenge referenced by the URL,
// returning the challenge itself so callers can validate against its fields
// (date, play seconds, ...) without a second round-trip.
func (s *Server) challengeIDFromPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, models.DailyChallenge, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] challengeIDFromPath: error invalid challenge id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid challenge id")
		return uuid.Nil, models.DailyChallenge{}, false
	}
	ch, err := s.store.GetChallenge(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			log.Printf("[DEBUG] challengeIDFromPath: challenge not found challengeID=%s", id)
			httpx.Error(w, http.StatusNotFound, "not_found", "challenge not found")
			return uuid.Nil, models.DailyChallenge{}, false
		}
		log.Printf("[DEBUG] challengeIDFromPath: error GetChallenge challengeID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load challenge")
		return uuid.Nil, models.DailyChallenge{}, false
	}
	return id, ch, true
}

// challengeActiveWindow is how far back a daily challenge stays startable /
// completable. Without this, any past challenge id could be replayed
// indefinitely to farm XP/expedition stars.
const challengeActiveWindow = 2 * 24 * time.Hour

// challengeIsActive reports whether a challenge's date is still within the
// window a user is allowed to start/complete it in (today, or a small grace
// window for recent days; never a future date).
func challengeIsActive(c models.DailyChallenge) bool {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	date := c.ChallengeDate.UTC().Truncate(24 * time.Hour)
	if date.After(today) {
		return false
	}
	return today.Sub(date) <= challengeActiveWindow
}

// handleStartChallenge marks the challenge as started (in_progress) for the user.
//
// @Summary      Start challenge
// @Description  Marks the challenge in_progress. Idempotent; never resets a finished result.
// @Tags         challenges
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Challenge id (UUID)"
// @Success      200  {object}  models.ChallengeResult
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/challenges/{id}/start [post]
func (s *Server) handleStartChallenge(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	id, ch, ok := s.challengeIDFromPath(w, r)
	if !ok {
		return
	}
	log.Printf("[DEBUG] handleStartChallenge: start userID=%s challengeID=%s", uid, id)
	if !challengeIsActive(ch) {
		log.Printf("[DEBUG] handleStartChallenge: challenge no longer active challengeID=%s date=%s", id, ch.ChallengeDate)
		httpx.Error(w, http.StatusForbidden, "expired", "challenge is no longer active")
		return
	}
	res, err := s.store.StartResult(r.Context(), uid, id)
	if err != nil {
		log.Printf("[DEBUG] handleStartChallenge: error StartResult userID=%s challengeID=%s: %v", uid, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not start challenge")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("challenge", "start").Inc()
	log.Printf("[DEBUG] handleStartChallenge: success userID=%s challengeID=%s", uid, id)
	httpx.JSON(w, http.StatusOK, res)
}

type completeChallengeRequest struct {
	ElapsedSeconds int  `json:"elapsedSeconds"`
	CorrectPieces  *int `json:"correctPieces"` // defaults to full grid when omitted
}

type failChallengeRequest struct {
	CorrectPieces int `json:"correctPieces"` // pieces placed before quitting
}

// completeChallengeResponse is the completion result plus the XP awarded and
// the user's resulting level progress (for the win screen's XP bar).
type completeChallengeResponse struct {
	models.ChallengeResult
	XPEarned       int `json:"xpEarned"`
	XP             int `json:"xp"`
	Level          int `json:"level"`
	XPIntoLevel    int `json:"xpIntoLevel"`
	XPForNextLevel int `json:"xpForNextLevel"`

	// PointsEarned is the unlock points this completion paid out (1 on the
	// first completion, 0 on a repeat call), and UnlockPoints the resulting
	// balance. Completing a daily challenge used to add an expedition star.
	PointsEarned int `json:"pointsEarned"`
	UnlockPoints int `json:"unlockPoints"`

	// HasQuiz tells the client to offer the bonus question on the win screen.
	HasQuiz bool `json:"hasQuiz"`
}

// unlockPointsPerChallenge is what completing a daily challenge pays, once.
const unlockPointsPerChallenge = 1

// handleCompleteChallenge marks an in-progress challenge as completed and awards XP.
//
// @Summary      Complete challenge
// @Description  Marks an in-progress challenge as completed (win) and awards XP.
// @Tags         challenges
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string                    true   "Challenge id (UUID)"
// @Param        request  body  completeChallengeRequest  false  "Elapsed seconds and correct pieces"
// @Success      200  {object}  completeChallengeResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/challenges/{id}/complete [post]
func (s *Server) handleCompleteChallenge(w http.ResponseWriter, r *http.Request) {
	id, ch, ok := s.challengeIDFromPath(w, r)
	if !ok {
		return
	}
	userID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleCompleteChallenge: start userID=%s challengeID=%s", userID, id)
	if !challengeIsActive(ch) {
		log.Printf("[DEBUG] handleCompleteChallenge: challenge no longer active challengeID=%s", id)
		httpx.Error(w, http.StatusForbidden, "expired", "challenge is no longer active")
		return
	}
	var req completeChallengeRequest
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleCompleteChallenge: error decoding request body challengeID=%s", id)
		return
	}
	if req.ElapsedSeconds < 0 || req.ElapsedSeconds > ch.PlaySeconds {
		log.Printf("[DEBUG] handleCompleteChallenge: error invalid elapsed time elapsedSeconds=%d playSeconds=%d", req.ElapsedSeconds, ch.PlaySeconds)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid elapsed time")
		return
	}

	// Completing places every piece; default to the full grid.
	correct := 0
	if req.CorrectPieces != nil {
		correct = *req.CorrectPieces
	}
	xpReward := ch.XPReward
	if correct <= 0 || correct > ch.Pieces() {
		correct = ch.Pieces()
	}

	res, transitioned, err := s.store.CompleteResult(r.Context(), userID, id, req.ElapsedSeconds, correct)
	if err != nil {
		log.Printf("[DEBUG] handleCompleteChallenge: error CompleteResult userID=%s challengeID=%s: %v", userID, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not complete challenge")
		return
	}

	resp := completeChallengeResponse{ChallengeResult: res, HasQuiz: ch.HasQuiz()}
	// Award XP only on the first transition to completed (idempotent).
	if transitioned && xpReward > 0 {
		if total, aerr := s.store.AddXP(r.Context(), userID, xpReward); aerr == nil {
			level, into, next := models.LevelFromXP(total)
			resp.XPEarned = xpReward
			resp.XP = total
			resp.Level = level
			resp.XPIntoLevel = into
			resp.XPForNextLevel = next
			log.Printf("[DEBUG] handleCompleteChallenge: XP awarded userID=%s challengeID=%s xpEarned=%d totalXP=%d", userID, id, xpReward, total)
		} else {
			log.Printf("[DEBUG] handleCompleteChallenge: error AddXP userID=%s: %v", userID, aerr)
		}
	}
	// Unlock point, also only on the first transition — the transition guard in
	// CompleteResult is what keeps this from being farmable.
	if transitioned {
		if balance, perr := s.store.AddUnlockPoints(r.Context(), userID, unlockPointsPerChallenge); perr == nil {
			resp.PointsEarned = unlockPointsPerChallenge
			resp.UnlockPoints = balance
			log.Printf("[DEBUG] handleCompleteChallenge: unlock point awarded userID=%s challengeID=%s balance=%d", userID, id, balance)
		} else {
			log.Printf("[DEBUG] handleCompleteChallenge: error AddUnlockPoints userID=%s: %v", userID, perr)
		}
	} else if balance, perr := s.store.GetUnlockPoints(r.Context(), userID); perr == nil {
		resp.UnlockPoints = balance
	}
	metrics.GameEventsTotal.WithLabelValues("challenge", "complete").Inc()
	log.Printf("[DEBUG] handleCompleteChallenge: success userID=%s challengeID=%s transitioned=%t correctPieces=%d", userID, id, transitioned, correct)
	httpx.JSON(w, http.StatusOK, resp)
}

// handleFailChallenge marks an in-progress challenge as failed (quit or lost).
// This is what locks the day's reputation to "Failed".
//
// @Summary      Fail challenge
// @Description  Marks an in-progress challenge as failed (user quit or lost); records pieces placed so far.
// @Tags         challenges
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string                 true   "Challenge id (UUID)"
// @Param        request  body  failChallengeRequest   false  "Correct pieces placed so far"
// @Success      200  {object}  models.ChallengeResult
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/challenges/{id}/fail [post]
func (s *Server) handleFailChallenge(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.challengeIDFromPath(w, r)
	if !ok {
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleFailChallenge: start userID=%s challengeID=%s", uid, id)
	var req failChallengeRequest
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleFailChallenge: error decoding request body challengeID=%s", id)
		return
	}
	if req.CorrectPieces < 0 {
		req.CorrectPieces = 0
	}
	res, err := s.store.FailResult(r.Context(), uid, id, req.CorrectPieces)
	if err != nil {
		log.Printf("[DEBUG] handleFailChallenge: error FailResult userID=%s challengeID=%s: %v", uid, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update challenge")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("challenge", "fail").Inc()
	log.Printf("[DEBUG] handleFailChallenge: success userID=%s challengeID=%s correctPieces=%d", uid, id, req.CorrectPieces)
	httpx.JSON(w, http.StatusOK, res)
}

// --- Bonus quiz --------------------------------------------------------------

// challengeQuizResponse is the question as a player may see it: the two
// options, but never which one is right — that would hand out the bonus point
// for free. CorrectOption is filled in only once the player has answered.
type challengeQuizResponse struct {
	Question       string `json:"question"`
	OptionA        string `json:"optionA"`
	OptionB        string `json:"optionB"`
	Answered       bool   `json:"answered"`
	AnsweredOption int    `json:"answeredOption"` // -1 while unanswered
	Correct        bool   `json:"correct"`        // meaningful once answered
	CorrectOption  int    `json:"correctOption"`  // -1 until answered
	PointsEarned   int    `json:"pointsEarned"`   // this call only
	UnlockPoints   int    `json:"unlockPoints"`   // resulting balance
}

type answerQuizRequest struct {
	Option int `json:"option"` // 0 = A, 1 = B
}

// handleChallengeQuiz returns the challenge's bonus question for the win screen.
//
// @Summary      Challenge bonus quiz
// @Description  The two-option bonus question for a challenge, plus whether the user already answered it. 404 when the challenge has no quiz.
// @Tags         challenges
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Challenge id (UUID)"
// @Success      200  {object}  challengeQuizResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/challenges/{id}/quiz [get]
func (s *Server) handleChallengeQuiz(w http.ResponseWriter, r *http.Request) {
	id, ch, ok := s.challengeIDFromPath(w, r)
	if !ok {
		return
	}
	userID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleChallengeQuiz: start userID=%s challengeID=%s", userID, id)
	if !ch.HasQuiz() {
		log.Printf("[DEBUG] handleChallengeQuiz: no quiz configured challengeID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "challenge has no quiz")
		return
	}

	resp := challengeQuizResponse{
		Question:       ch.QuizQuestion,
		OptionA:        ch.QuizOptionA,
		OptionB:        ch.QuizOptionB,
		AnsweredOption: -1,
		CorrectOption:  -1,
	}
	res, err := s.store.GetResult(r.Context(), userID, id)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleChallengeQuiz: error GetResult userID=%s challengeID=%s: %v", userID, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load result")
		return
	}
	if err == nil && res.QuizAnswer != nil {
		resp.Answered = true
		resp.AnsweredOption = *res.QuizAnswer
		resp.Correct = *res.QuizAnswer == ch.QuizCorrectOption
		resp.CorrectOption = ch.QuizCorrectOption
	}
	if balance, perr := s.store.GetUnlockPoints(r.Context(), userID); perr == nil {
		resp.UnlockPoints = balance
	}
	log.Printf("[DEBUG] handleChallengeQuiz: success userID=%s challengeID=%s answered=%t", userID, id, resp.Answered)
	httpx.JSON(w, http.StatusOK, resp)
}

// handleAnswerChallengeQuiz records the player's answer and grants one unlock
// point for a correct one — once per challenge, only after completing it.
//
// @Summary      Answer challenge bonus quiz
// @Description  Records the answer to the bonus question; a correct first answer grants +1 unlock point.
// @Tags         challenges
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string             true  "Challenge id (UUID)"
// @Param        request  body  answerQuizRequest  true  "Chosen option (0 = A, 1 = B)"
// @Success      200  {object}  challengeQuizResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      403  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/challenges/{id}/quiz [post]
func (s *Server) handleAnswerChallengeQuiz(w http.ResponseWriter, r *http.Request) {
	id, ch, ok := s.challengeIDFromPath(w, r)
	if !ok {
		return
	}
	userID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAnswerChallengeQuiz: start userID=%s challengeID=%s", userID, id)
	if !ch.HasQuiz() {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: no quiz configured challengeID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "challenge has no quiz")
		return
	}
	if !challengeIsActive(ch) {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: challenge no longer active challengeID=%s", id)
		httpx.Error(w, http.StatusForbidden, "expired", "challenge is no longer active")
		return
	}

	var req answerQuizRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: error decoding request body challengeID=%s", id)
		return
	}
	if req.Option != 0 && req.Option != 1 {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: error invalid option=%d challengeID=%s", req.Option, id)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "option must be 0 or 1")
		return
	}

	// The quiz is a reward for solving the puzzle, not a standalone quiz: only
	// a completed result can answer it.
	res, err := s.store.GetResult(r.Context(), userID, id)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: no result userID=%s challengeID=%s", userID, id)
		httpx.Error(w, http.StatusForbidden, "not_completed", "complete the challenge first")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: error GetResult userID=%s challengeID=%s: %v", userID, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load result")
		return
	}
	if res.Status != "completed" {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: result not completed status=%s userID=%s challengeID=%s", res.Status, userID, id)
		httpx.Error(w, http.StatusForbidden, "not_completed", "complete the challenge first")
		return
	}

	correct := req.Option == ch.QuizCorrectOption
	granted, stored, err := s.store.AnswerChallengeQuiz(r.Context(), userID, id, req.Option, correct)
	if err != nil {
		log.Printf("[DEBUG] handleAnswerChallengeQuiz: error AnswerChallengeQuiz userID=%s challengeID=%s: %v", userID, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not record answer")
		return
	}

	// A second attempt changes nothing: report the answer already on file.
	answered := req.Option
	if stored != nil {
		answered = *stored
	}
	resp := challengeQuizResponse{
		Question:       ch.QuizQuestion,
		OptionA:        ch.QuizOptionA,
		OptionB:        ch.QuizOptionB,
		Answered:       true,
		AnsweredOption: answered,
		Correct:        answered == ch.QuizCorrectOption,
		CorrectOption:  ch.QuizCorrectOption,
	}
	if granted {
		resp.PointsEarned = 1
	}
	if balance, perr := s.store.GetUnlockPoints(r.Context(), userID); perr == nil {
		resp.UnlockPoints = balance
	}
	log.Printf("[DEBUG] handleAnswerChallengeQuiz: success userID=%s challengeID=%s option=%d correct=%t granted=%t",
		userID, id, req.Option, resp.Correct, granted)
	httpx.JSON(w, http.StatusOK, resp)
}
