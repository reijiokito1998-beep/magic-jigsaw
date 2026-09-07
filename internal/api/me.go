package api

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

// handleMe returns the authenticated user's profile.
//
// @Summary      Current user
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  models.User
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/me [get]
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleMe: start userID=%s", uid)
	user, err := s.store.GetUser(r.Context(), uid)
	if err != nil {
		log.Printf("[DEBUG] handleMe: user not found userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	httpx.JSON(w, http.StatusOK, user)
}

type notificationsResponse struct {
	// HasNew is true when today's challenge exists and the user hasn't played it.
	HasNew    bool                  `json:"hasNew"`
	Challenge *models.ChallengeView `json:"challenge,omitempty"`
}

// handleNotifications reports whether there is a fresh daily challenge for the
// user (i.e. today's challenge with no result yet).
//
// @Summary      Notifications
// @Description  Returns whether today's challenge exists and is unplayed by the user.
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  notificationsResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      500  {object}  httpx.ErrorBody
// @Router       /api/v1/me/notifications [get]
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	today := time.Now().UTC().Format("2006-01-02")
	log.Printf("[DEBUG] handleNotifications: start userID=%s date=%s", uid, today)
	challenge, err := s.store.GetChallengeByDate(r.Context(), today)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleNotifications: no challenge for today date=%s", today)
		httpx.JSON(w, http.StatusOK, notificationsResponse{HasNew: false})
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleNotifications: error GetChallengeByDate date=%s: %v", today, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load challenge")
		return
	}

	view, err := s.buildChallengeView(r.Context(), challenge)
	if err != nil {
		log.Printf("[DEBUG] handleNotifications: error buildChallengeView challengeID=%s: %v", challenge.ID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load result")
		return
	}
	log.Printf("[DEBUG] handleNotifications: success userID=%s hasNew=%t", uid, view.Result == nil)
	httpx.JSON(w, http.StatusOK, notificationsResponse{HasNew: view.Result == nil, Challenge: &view})
}

// handleMyResults returns the user's reputation entries (for the future
// "Danh vọng" screen).
//
// @Summary      Reputation history
// @Description  Returns the user's daily challenge results ("Danh vọng").
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  resultsResponse
// @Failure      401  {object}  httpx.ErrorBody
// @Failure      500  {object}  httpx.ErrorBody
// @Router       /api/v1/me/results [get]
func (s *Server) handleMyResults(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleMyResults: start userID=%s", uid)
	results, err := s.store.ListResults(r.Context(), uid)
	if err != nil {
		log.Printf("[DEBUG] handleMyResults: error ListResults userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load results")
		return
	}
	log.Printf("[DEBUG] handleMyResults: success userID=%s count=%d", uid, len(results))
	httpx.JSON(w, http.StatusOK, resultsResponse{Results: results})
}
