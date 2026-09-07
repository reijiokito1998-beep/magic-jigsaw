package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

type adminDungeonsResponse struct {
	Dungeons []models.AdminDungeon `json:"dungeons"`
	pageMeta
}

// handleAdminListDungeons lists every dungeon (including inactive ones) with
// its full layout and monsters.
//
// @Summary      List dungeons (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  adminDungeonsResponse
// @Router       /api/v1/admin/dungeons [get]
func (s *Server) handleAdminListDungeons(w http.ResponseWriter, r *http.Request) {
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleAdminListDungeons: start adminID=%s page=%d limit=%d",
		httpx.UserIDFrom(r.Context()), page.Number, page.Limit)
	list, total, err := s.store.AdminListDungeons(r.Context(), page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleAdminListDungeons: error AdminListDungeons: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load dungeons")
		return
	}
	log.Printf("[DEBUG] handleAdminListDungeons: success count=%d total=%d", len(list), total)
	httpx.JSON(w, http.StatusOK, adminDungeonsResponse{Dungeons: list, pageMeta: metaFor(page, total)})
}

// adminDungeonRequest is the admin-authored dungeon layout body, shared by
// create and full-replace update.
type adminDungeonRequest struct {
	Title               string                       `json:"title"`
	XPReward            int                          `json:"xpReward"`
	Active              bool                         `json:"active"`
	Start               models.Cell                  `json:"start"`
	Treasure            models.Cell                  `json:"treasure"`
	Path                []models.Cell                `json:"path"`
	Monsters            []models.DungeonMonsterInput `json:"monsters"`
	TreasureImageID     uuid.UUID                    `json:"treasureImageId"`
	TreasureGridCols    int                          `json:"treasureGridCols"`
	TreasureGridRows    int                          `json:"treasureGridRows"`
	TreasurePlaySeconds int                          `json:"treasurePlaySeconds"`
}

// validateDungeonRequest checks structural layout rules and that every
// monster's (and the treasure's) image exists, writing a 400 response and
// returning ok=false on the first failure. A treasure puzzle image is
// mandatory for admin-authored dungeons.
func (s *Server) validateDungeonRequest(w http.ResponseWriter, r *http.Request, req adminDungeonRequest) (repository.SaveDungeonParams, bool) {
	if req.Title == "" {
		log.Printf("[DEBUG] validateDungeonRequest: error missing title")
		httpx.Error(w, http.StatusBadRequest, "bad_request", "title is required")
		return repository.SaveDungeonParams{}, false
	}
	if req.XPReward <= 0 {
		log.Printf("[DEBUG] validateDungeonRequest: error xpReward must be positive xpReward=%d", req.XPReward)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "xpReward must be positive")
		return repository.SaveDungeonParams{}, false
	}
	if req.TreasureImageID == uuid.Nil {
		log.Printf("[DEBUG] validateDungeonRequest: error missing treasure image")
		httpx.Error(w, http.StatusBadRequest, "bad_request", "treasureImageId is required")
		return repository.SaveDungeonParams{}, false
	}

	layout := models.DungeonLayout{
		Start: req.Start, Treasure: req.Treasure, Path: req.Path, Monsters: req.Monsters,
		TreasureImageID: req.TreasureImageID, TreasureGridCols: req.TreasureGridCols,
		TreasureGridRows: req.TreasureGridRows, TreasurePlaySeconds: req.TreasurePlaySeconds,
	}
	if err := models.ValidateDungeonLayout(layout); err != nil {
		log.Printf("[DEBUG] validateDungeonRequest: error invalid layout: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return repository.SaveDungeonParams{}, false
	}

	for i, m := range req.Monsters {
		if _, err := s.store.GetImage(r.Context(), m.ImageID); err != nil {
			log.Printf("[DEBUG] validateDungeonRequest: error monster %d image not found imageID=%s: %v", i, m.ImageID, err)
			httpx.Error(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("monster %d: image not found", i))
			return repository.SaveDungeonParams{}, false
		}
	}

	if _, err := s.store.GetImage(r.Context(), req.TreasureImageID); err != nil {
		log.Printf("[DEBUG] validateDungeonRequest: error treasure image not found imageID=%s: %v", req.TreasureImageID, err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "treasure: image not found")
		return repository.SaveDungeonParams{}, false
	}

	return repository.SaveDungeonParams{
		Title: req.Title, XPReward: req.XPReward, Active: req.Active,
		Start: req.Start, Treasure: req.Treasure, Path: req.Path, Monsters: req.Monsters,
		TreasureImageID: req.TreasureImageID, TreasureGridCols: req.TreasureGridCols,
		TreasureGridRows: req.TreasureGridRows, TreasurePlaySeconds: req.TreasurePlaySeconds,
	}, true
}

// handleAdminCreateDungeon creates a dungeon layout with its monsters.
//
// @Summary      Create dungeon (admin)
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body  adminDungeonRequest  true  "Dungeon layout"
// @Success      201  {object}  models.AdminDungeon
// @Failure      400  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/dungeons [post]
func (s *Server) handleAdminCreateDungeon(w http.ResponseWriter, r *http.Request) {
	adminID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAdminCreateDungeon: start adminID=%s", adminID)
	var req adminDungeonRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAdminCreateDungeon: error decoding request body adminID=%s", adminID)
		return
	}
	params, ok := s.validateDungeonRequest(w, r, req)
	if !ok {
		return
	}

	id, err := s.store.CreateDungeon(r.Context(), params)
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateDungeon: error CreateDungeon: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create dungeon")
		return
	}
	d, err := s.store.GetAdminDungeon(r.Context(), id)
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateDungeon: error GetAdminDungeon dungeonID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load created dungeon")
		return
	}
	log.Printf("[DEBUG] handleAdminCreateDungeon: success dungeonID=%s", id)
	httpx.JSON(w, http.StatusCreated, d)
}

// handleAdminUpdateDungeon fully replaces a dungeon's layout and monsters.
// Any saved player progress for the dungeon is dropped since old positions
// are no longer valid against the new map.
//
// @Summary      Update dungeon (admin)
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string               true  "Dungeon id (UUID)"
// @Param        request  body  adminDungeonRequest  true  "Dungeon layout"
// @Success      200  {object}  models.AdminDungeon
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/dungeons/{id} [put]
func (s *Server) handleAdminUpdateDungeon(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateDungeon: error invalid dungeon id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid dungeon id")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateDungeon: start dungeonID=%s adminID=%s", id, httpx.UserIDFrom(r.Context()))
	var req adminDungeonRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAdminUpdateDungeon: error decoding request body dungeonID=%s", id)
		return
	}
	params, ok := s.validateDungeonRequest(w, r, req)
	if !ok {
		return
	}

	if err := s.store.UpdateDungeon(r.Context(), id, params); errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleAdminUpdateDungeon: dungeon not found dungeonID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "dungeon not found")
		return
	} else if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateDungeon: error UpdateDungeon dungeonID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update dungeon")
		return
	}

	d, err := s.store.GetAdminDungeon(r.Context(), id)
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateDungeon: error GetAdminDungeon dungeonID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load updated dungeon")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateDungeon: success dungeonID=%s", id)
	httpx.JSON(w, http.StatusOK, d)
}

// handleAdminDeleteDungeon deletes a dungeon (its monsters/progress cascade).
//
// @Summary      Delete dungeon (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Dungeon id (UUID)"
// @Success      200  {object}  map[string]bool
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/dungeons/{id} [delete]
func (s *Server) handleAdminDeleteDungeon(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminDeleteDungeon: error invalid dungeon id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid dungeon id")
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteDungeon: start dungeonID=%s adminID=%s", id, httpx.UserIDFrom(r.Context()))
	if err := s.store.DeleteDungeon(r.Context(), id); errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleAdminDeleteDungeon: dungeon not found dungeonID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "dungeon not found")
		return
	} else if err != nil {
		log.Printf("[DEBUG] handleAdminDeleteDungeon: error DeleteDungeon dungeonID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete dungeon")
		return
	}
	log.Printf("[DEBUG] handleAdminDeleteDungeon: success dungeonID=%s", id)
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
