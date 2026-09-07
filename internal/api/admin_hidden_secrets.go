package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

type adminHiddenSecretsResponse struct {
	Secrets []models.AdminHiddenSecret `json:"secrets"`
	pageMeta
}

// handleAdminListHiddenSecrets lists every hidden secret, including inactive
// ones.
//
// @Summary      List hidden secrets (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  adminHiddenSecretsResponse
// @Router       /api/v1/admin/hidden-secrets [get]
func (s *Server) handleAdminListHiddenSecrets(w http.ResponseWriter, r *http.Request) {
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleAdminListHiddenSecrets: start adminID=%s page=%d limit=%d",
		httpx.UserIDFrom(r.Context()), page.Number, page.Limit)
	list, total, err := s.store.AdminListHiddenSecrets(r.Context(), page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleAdminListHiddenSecrets: error AdminListHiddenSecrets: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load hidden secrets")
		return
	}
	log.Printf("[DEBUG] handleAdminListHiddenSecrets: success count=%d total=%d", len(list), total)
	httpx.JSON(w, http.StatusOK, adminHiddenSecretsResponse{Secrets: s.decorateAdminHiddenSecrets(list), pageMeta: metaFor(page, total)})
}

// adminHiddenSecretRequest is the admin-authored hidden secret body, shared by
// create and update.
type adminHiddenSecretRequest struct {
	Title           string    `json:"title"`
	Subtitle        string    `json:"subtitle"`
	ImageID         uuid.UUID `json:"imageId"`
	XPReward        int       `json:"xpReward"`
	TileGridCols    int       `json:"tileGridCols"`
	TileGridRows    int       `json:"tileGridRows"`
	TilePlaySeconds int       `json:"tilePlaySeconds"`
	Active          bool      `json:"active"`
}

// validateHiddenSecretRequest checks the config rules and that the picture
// exists, writing a 400 response and returning ok=false on the first failure.
func (s *Server) validateHiddenSecretRequest(w http.ResponseWriter, r *http.Request, req adminHiddenSecretRequest) (repository.SaveHiddenSecretParams, bool) {
	cfg := models.HiddenSecretConfig{
		Title: req.Title, Subtitle: req.Subtitle, ImageID: req.ImageID,
		XPReward: req.XPReward, TileGridCols: req.TileGridCols,
		TileGridRows: req.TileGridRows, TilePlaySeconds: req.TilePlaySeconds,
	}
	if err := models.ValidateHiddenSecret(cfg); err != nil {
		log.Printf("[DEBUG] validateHiddenSecretRequest: error invalid config: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return repository.SaveHiddenSecretParams{}, false
	}
	if _, err := s.store.GetImage(r.Context(), req.ImageID); err != nil {
		log.Printf("[DEBUG] validateHiddenSecretRequest: error image not found imageID=%s: %v", req.ImageID, err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "image not found")
		return repository.SaveHiddenSecretParams{}, false
	}
	return repository.SaveHiddenSecretParams{
		Title: req.Title, Subtitle: req.Subtitle, ImageID: req.ImageID,
		XPReward: req.XPReward, TileGridCols: req.TileGridCols,
		TileGridRows: req.TileGridRows, TilePlaySeconds: req.TilePlaySeconds,
		Active: req.Active,
	}, true
}

// handleAdminCreateHiddenSecret creates a hidden secret.
//
// @Summary      Create hidden secret (admin)
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body  adminHiddenSecretRequest  true  "Hidden secret"
// @Success      201  {object}  models.AdminHiddenSecret
// @Failure      400  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/hidden-secrets [post]
func (s *Server) handleAdminCreateHiddenSecret(w http.ResponseWriter, r *http.Request) {
	adminID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAdminCreateHiddenSecret: start adminID=%s", adminID)
	var req adminHiddenSecretRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAdminCreateHiddenSecret: error decoding request body adminID=%s", adminID)
		return
	}
	params, ok := s.validateHiddenSecretRequest(w, r, req)
	if !ok {
		return
	}

	id, err := s.store.CreateHiddenSecret(r.Context(), params)
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateHiddenSecret: error CreateHiddenSecret: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create hidden secret")
		return
	}
	h, err := s.store.GetAdminHiddenSecret(r.Context(), id)
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateHiddenSecret: error GetAdminHiddenSecret secretID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load created hidden secret")
		return
	}
	log.Printf("[DEBUG] handleAdminCreateHiddenSecret: success secretID=%s", id)
	httpx.JSON(w, http.StatusCreated, h)
}

// handleAdminUpdateHiddenSecret updates a hidden secret's configuration.
// Changing its picture resets every player's revealed tiles, since the crops
// they solved belonged to the old image.
//
// @Summary      Update hidden secret (admin)
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string                    true  "Hidden secret id (UUID)"
// @Param        request  body  adminHiddenSecretRequest  true  "Hidden secret"
// @Success      200  {object}  models.AdminHiddenSecret
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/hidden-secrets/{id} [put]
func (s *Server) handleAdminUpdateHiddenSecret(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateHiddenSecret: error invalid secret id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid hidden secret id")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateHiddenSecret: start secretID=%s adminID=%s", id, httpx.UserIDFrom(r.Context()))
	var req adminHiddenSecretRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAdminUpdateHiddenSecret: error decoding request body secretID=%s", id)
		return
	}
	params, ok := s.validateHiddenSecretRequest(w, r, req)
	if !ok {
		return
	}

	if err := s.store.UpdateHiddenSecret(r.Context(), id, params); errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleAdminUpdateHiddenSecret: secret not found secretID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "hidden secret not found")
		return
	} else if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateHiddenSecret: error UpdateHiddenSecret secretID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update hidden secret")
		return
	}

	h, err := s.store.GetAdminHiddenSecret(r.Context(), id)
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateHiddenSecret: error GetAdminHiddenSecret secretID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load updated hidden secret")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateHiddenSecret: success secretID=%s", id)
	httpx.JSON(w, http.StatusOK, h)
}

// handleAdminDeleteHiddenSecret deletes a hidden secret (progress and unlocks
// cascade).
//
// @Summary      Delete hidden secret (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Hidden secret id (UUID)"
// @Success      200  {object}  map[string]bool
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/hidden-secrets/{id} [delete]
func (s *Server) handleAdminDeleteHiddenSecret(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminDeleteHiddenSecret: error invalid secret id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid hidden secret id")
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteHiddenSecret: start secretID=%s adminID=%s", id, httpx.UserIDFrom(r.Context()))
	if err := s.store.DeleteHiddenSecret(r.Context(), id); errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleAdminDeleteHiddenSecret: secret not found secretID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "hidden secret not found")
		return
	} else if err != nil {
		log.Printf("[DEBUG] handleAdminDeleteHiddenSecret: error DeleteHiddenSecret secretID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete hidden secret")
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteHiddenSecret: success secretID=%s", id)
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
