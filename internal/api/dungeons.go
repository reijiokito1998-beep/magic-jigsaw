package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/httpx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/models"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
)

type dungeonsResponse struct {
	Dungeons []models.Dungeon `json:"dungeons"`
	pageMeta
}

// handleListDungeons lists active dungeons with the caller's progress.
//
// @Summary      List dungeons
// @Tags         dungeons
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  dungeonsResponse
// @Router       /api/v1/dungeons [get]
func (s *Server) handleListDungeons(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleListDungeons: start userID=%s page=%d limit=%d", uid, page.Number, page.Limit)
	list, total, err := s.store.ListDungeons(r.Context(), uid, page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleListDungeons: error ListDungeons userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load dungeons")
		return
	}
	log.Printf("[DEBUG] handleListDungeons: success userID=%s count=%d total=%d", uid, len(list), total)
	httpx.JSON(w, http.StatusOK, dungeonsResponse{Dungeons: list, pageMeta: metaFor(page, total)})
}

// handleGetDungeon returns a dungeon's full layout with the caller's progress
// and current energy (unlock points).
//
// @Summary      Get dungeon
// @Tags         dungeons
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Dungeon id (UUID)"
// @Success      200  {object}  models.DungeonDetail
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/dungeons/{id} [get]
func (s *Server) handleGetDungeon(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleGetDungeon: error invalid dungeon id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid dungeon id")
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleGetDungeon: start userID=%s dungeonID=%s", uid, id)
	d, err := s.store.GetDungeon(r.Context(), id, uid)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleGetDungeon: dungeon not found dungeonID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "dungeon not found")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleGetDungeon: error GetDungeon dungeonID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load dungeon")
		return
	}
	log.Printf("[DEBUG] handleGetDungeon: success userID=%s dungeonID=%s", uid, id)
	httpx.JSON(w, http.StatusOK, d)
}

type dungeonMoveRequest struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type dungeonMoveResponse struct {
	PosRow  int                    `json:"posRow"`
	PosCol  int                    `json:"posCol"`
	Energy  int                    `json:"energy"`
	Monster *models.DungeonMonster `json:"monster"`
}

// handleMoveDungeon moves the player onto an adjacent path cell, spending 1
// unlock point. Returns the (undefeated) monster occupying the target cell,
// if any.
//
// @Summary      Move in dungeon
// @Tags         dungeons
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string              true  "Dungeon id (UUID)"
// @Param        request  body  dungeonMoveRequest  true  "Target cell"
// @Success      200  {object}  dungeonMoveResponse
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Failure      422  {object}  httpx.ErrorBody
// @Router       /api/v1/dungeons/{id}/move [post]
func (s *Server) handleMoveDungeon(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleMoveDungeon: error invalid dungeon id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid dungeon id")
		return
	}
	var req dungeonMoveRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleMoveDungeon: error decoding request body dungeonID=%s", id)
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleMoveDungeon: start userID=%s dungeonID=%s target=(%d,%d)", uid, id, req.Row, req.Col)

	res, ok, err := s.store.MoveDungeon(r.Context(), uid, id, models.Cell{Row: req.Row, Col: req.Col})
	switch {
	case errors.Is(err, repository.ErrNotFound):
		log.Printf("[DEBUG] handleMoveDungeon: dungeon not found dungeonID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "dungeon not found")
		return
	case errors.Is(err, repository.ErrDungeonInactive):
		log.Printf("[DEBUG] handleMoveDungeon: dungeon inactive dungeonID=%s", id)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "dungeon is inactive")
		return
	case errors.Is(err, repository.ErrDungeonCompleted):
		log.Printf("[DEBUG] handleMoveDungeon: dungeon already completed dungeonID=%s", id)
		httpx.Error(w, http.StatusConflict, "already_completed", "dungeon already completed")
		return
	case errors.Is(err, repository.ErrInvalidMove):
		log.Printf("[DEBUG] handleMoveDungeon: invalid move dungeonID=%s target=(%d,%d)", id, req.Row, req.Col)
		httpx.Error(w, http.StatusUnprocessableEntity, "invalid_move", "target cell is not a valid move")
		return
	case err != nil:
		log.Printf("[DEBUG] handleMoveDungeon: error MoveDungeon userID=%s dungeonID=%s: %v", uid, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not move")
		return
	}
	if !ok {
		log.Printf("[DEBUG] handleMoveDungeon: insufficient unlock points userID=%s dungeonID=%s", uid, id)
		httpx.Error(w, http.StatusConflict, "insufficient_points", "not enough unlock points")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("dungeon", "move").Inc()
	log.Printf("[DEBUG] handleMoveDungeon: success userID=%s dungeonID=%s pos=(%d,%d) energy=%d", uid, id, res.PosRow, res.PosCol, res.Energy)
	httpx.JSON(w, http.StatusOK, dungeonMoveResponse{
		PosRow: res.PosRow, PosCol: res.PosCol, Energy: res.Energy, Monster: res.Monster,
	})
}

// handleRetreatDungeon moves the player back to their previous cell for free
// (a no-op if there is no previous cell).
//
// @Summary      Retreat in dungeon
// @Tags         dungeons
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Dungeon id (UUID)"
// @Success      200  {object}  dungeonMoveResponse
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/dungeons/{id}/retreat [post]
func (s *Server) handleRetreatDungeon(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleRetreatDungeon: error invalid dungeon id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid dungeon id")
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleRetreatDungeon: start userID=%s dungeonID=%s", uid, id)
	posRow, posCol, energy, err := s.store.RetreatDungeon(r.Context(), uid, id)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleRetreatDungeon: dungeon not found dungeonID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "dungeon not found")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleRetreatDungeon: error RetreatDungeon userID=%s dungeonID=%s: %v", uid, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not retreat")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("dungeon", "retreat").Inc()
	log.Printf("[DEBUG] handleRetreatDungeon: success userID=%s dungeonID=%s pos=(%d,%d) energy=%d", uid, id, posRow, posCol, energy)
	httpx.JSON(w, http.StatusOK, dungeonMoveResponse{PosRow: posRow, PosCol: posCol, Energy: energy, Monster: nil})
}

type dungeonDefeatResponse struct {
	Defeated []string `json:"defeated"`
}

// handleCompleteDungeonMonster marks a monster defeated, requiring the caller
// to currently be at that monster's cell.
//
// @Summary      Defeat dungeon monster
// @Tags         dungeons
// @Produce      json
// @Security     BearerAuth
// @Param        id         path  string  true  "Dungeon id (UUID)"
// @Param        monsterId  path  string  true  "Monster id (UUID)"
// @Success      200  {object}  dungeonDefeatResponse
// @Failure      404  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Failure      422  {object}  httpx.ErrorBody
// @Router       /api/v1/dungeons/{id}/monsters/{monsterId}/complete [post]
func (s *Server) handleCompleteDungeonMonster(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleCompleteDungeonMonster: error invalid dungeon id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid dungeon id")
		return
	}
	monsterID, err := uuid.Parse(r.PathValue("monsterId"))
	if err != nil {
		log.Printf("[DEBUG] handleCompleteDungeonMonster: error invalid monster id %q: %v", r.PathValue("monsterId"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid monster id")
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleCompleteDungeonMonster: start userID=%s dungeonID=%s monsterID=%s", uid, id, monsterID)

	defeated, err := s.store.CompleteDungeonMonster(r.Context(), uid, id, monsterID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		log.Printf("[DEBUG] handleCompleteDungeonMonster: monster not found dungeonID=%s monsterID=%s", id, monsterID)
		httpx.Error(w, http.StatusNotFound, "not_found", "monster not found")
		return
	case errors.Is(err, repository.ErrWrongCell):
		log.Printf("[DEBUG] handleCompleteDungeonMonster: player not at monster's cell userID=%s monsterID=%s", uid, monsterID)
		httpx.Error(w, http.StatusUnprocessableEntity, "wrong_cell", "player is not at this monster's cell")
		return
	case errors.Is(err, repository.ErrAlreadyDefeated):
		log.Printf("[DEBUG] handleCompleteDungeonMonster: monster already defeated monsterID=%s", monsterID)
		httpx.Error(w, http.StatusConflict, "already_defeated", "monster already defeated")
		return
	case err != nil:
		log.Printf("[DEBUG] handleCompleteDungeonMonster: error CompleteDungeonMonster userID=%s monsterID=%s: %v", uid, monsterID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not complete monster")
		return
	}
	metrics.GameEventsTotal.WithLabelValues("dungeon", "monster_defeated").Inc()
	log.Printf("[DEBUG] handleCompleteDungeonMonster: success userID=%s dungeonID=%s monsterID=%s defeatedCount=%d", uid, id, monsterID, len(defeated))
	httpx.JSON(w, http.StatusOK, dungeonDefeatResponse{Defeated: defeated})
}

// dungeonCompleteResponse mirrors storyCompleteResponse/expeditionCompleteResponse
// so the client can reuse its XP/level-up win-screen UI. Status is included
// alongside Completed for compatibility with clients that model this payload
// after models.ChallengeResult (which uses a "status" string).
type dungeonCompleteResponse struct {
	Completed      bool   `json:"completed"`
	Status         string `json:"status"`
	XPEarned       int    `json:"xpEarned"`
	XP             int    `json:"xp"`
	Level          int    `json:"level"`
	XPIntoLevel    int    `json:"xpIntoLevel"`
	XPForNextLevel int    `json:"xpForNextLevel"`
}

// handleCompleteDungeon completes the dungeon (player at the treasure cell,
// all monsters defeated) and awards its XP reward.
//
// @Summary      Complete dungeon
// @Tags         dungeons
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Dungeon id (UUID)"
// @Success      200  {object}  dungeonCompleteResponse
// @Failure      404  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Failure      422  {object}  httpx.ErrorBody
// @Router       /api/v1/dungeons/{id}/complete [post]
func (s *Server) handleCompleteDungeon(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleCompleteDungeon: error invalid dungeon id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid dungeon id")
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleCompleteDungeon: start userID=%s dungeonID=%s", uid, id)

	awarded, err := s.store.CompleteDungeon(r.Context(), uid, id)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		log.Printf("[DEBUG] handleCompleteDungeon: dungeon not found dungeonID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "dungeon not found")
		return
	case errors.Is(err, repository.ErrDungeonCompleted):
		log.Printf("[DEBUG] handleCompleteDungeon: dungeon already completed dungeonID=%s", id)
		httpx.Error(w, http.StatusConflict, "already_completed", "dungeon already completed")
		return
	case errors.Is(err, repository.ErrWrongCell):
		log.Printf("[DEBUG] handleCompleteDungeon: player not at treasure cell userID=%s dungeonID=%s", uid, id)
		httpx.Error(w, http.StatusUnprocessableEntity, "not_at_treasure", "player is not at the treasure cell")
		return
	case errors.Is(err, repository.ErrMonstersRemain):
		log.Printf("[DEBUG] handleCompleteDungeon: monsters remaining userID=%s dungeonID=%s", uid, id)
		httpx.Error(w, http.StatusUnprocessableEntity, "monsters_remaining", "not all monsters are defeated")
		return
	case err != nil:
		log.Printf("[DEBUG] handleCompleteDungeon: error CompleteDungeon userID=%s dungeonID=%s: %v", uid, id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not complete dungeon")
		return
	}

	resp := dungeonCompleteResponse{Completed: true, Status: models.StatusCompleted}
	total := 0
	if awarded > 0 {
		if t, e := s.store.AddXP(r.Context(), uid, awarded); e == nil {
			total = t
			resp.XPEarned = awarded
			log.Printf("[DEBUG] handleCompleteDungeon: XP awarded userID=%s dungeonID=%s xpEarned=%d totalXP=%d", uid, id, awarded, total)
		} else {
			log.Printf("[DEBUG] handleCompleteDungeon: error AddXP userID=%s: %v", uid, e)
		}
	} else {
		total, _ = s.store.GetXP(r.Context(), uid)
	}
	level, into, next := models.LevelFromXP(total)
	resp.XP = total
	resp.Level = level
	resp.XPIntoLevel = into
	resp.XPForNextLevel = next
	metrics.GameEventsTotal.WithLabelValues("dungeon", "complete").Inc()
	log.Printf("[DEBUG] handleCompleteDungeon: success userID=%s dungeonID=%s", uid, id)
	httpx.JSON(w, http.StatusOK, resp)
}

type dungeonTreasuresResponse struct {
	Treasures []models.DungeonTreasureUnlock `json:"treasures"`
}

// handleListDungeonTreasures lists the treasure images the caller has unlocked
// by completing dungeons, newest first.
//
// @Summary      List unlocked dungeon treasures
// @Tags         dungeons
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  dungeonTreasuresResponse
// @Router       /api/v1/dungeon-treasures [get]
func (s *Server) handleListDungeonTreasures(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleListDungeonTreasures: start userID=%s", uid)
	list, err := s.store.ListUnlockedTreasures(r.Context(), uid)
	if err != nil {
		log.Printf("[DEBUG] handleListDungeonTreasures: error ListUnlockedTreasures userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load dungeon treasures")
		return
	}
	log.Printf("[DEBUG] handleListDungeonTreasures: success userID=%s count=%d", uid, len(list))
	httpx.JSON(w, http.StatusOK, dungeonTreasuresResponse{Treasures: list})
}
