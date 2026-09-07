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

// storyLevelRangeMessage is the single wording used wherever levelRequired is
// rejected, so create and update explain the rule identically.
const storyLevelRangeMessage = "levelRequired must be between 1 and 100"

// validStoryLevel bounds a story's level gate to a level a player can actually
// reach: 1 is "open to everyone", models.MaxLevel is the ceiling.
func validStoryLevel(level int) bool {
	return level >= 1 && level <= models.MaxLevel
}

// handleAdminListStories lists all stories for the admin.
//
// @Summary      List stories (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  storiesResponse
// @Router       /api/v1/admin/stories [get]
func (s *Server) handleAdminListStories(w http.ResponseWriter, r *http.Request) {
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleAdminListStories: start adminID=%s page=%d limit=%d",
		httpx.UserIDFrom(r.Context()), page.Number, page.Limit)
	list, total, err := s.store.ListStories(r.Context(), uuid.Nil, page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleAdminListStories: error ListStories: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load stories")
		return
	}
	log.Printf("[DEBUG] handleAdminListStories: success count=%d total=%d", len(list), total)
	httpx.JSON(w, http.StatusOK, storiesResponse{Stories: s.decorateStories(list), pageMeta: metaFor(page, total)})
}

// handleAdminCreateStory uploads a cover image and creates a story.
//
// @Summary      Create story (admin)
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        file           formData  file    true   "Cover image (max 15MB)"
// @Param        title          formData  string  true   "Story title"
// @Param        category       formData  string  false  "Category"
// @Param        author         formData  string  false  "Author"
// @Param        description    formData  string  false  "Description"
// @Param        levelRequired  formData  int     false  "Player level needed to play it (default 1)"
// @Success      201  {object}  models.Story
// @Failure      400  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/stories [post]
func (s *Server) handleAdminCreateStory(w http.ResponseWriter, r *http.Request) {
	adminID := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleAdminCreateStory: start adminID=%s", adminID)
	// Cover from the library (imageId) or an uploaded file; parses the form.
	coverID, ok := s.resolveImageID(w, r, false)
	if !ok {
		log.Printf("[DEBUG] handleAdminCreateStory: resolveImageID failed adminID=%s", adminID)
		return
	}
	if r.FormValue("title") == "" {
		log.Printf("[DEBUG] handleAdminCreateStory: error missing title")
		httpx.Error(w, http.StatusBadRequest, "bad_request", "title is required")
		return
	}
	levelRequired := atoiDefault(r.FormValue("levelRequired"), 1)
	if !validStoryLevel(levelRequired) {
		log.Printf("[DEBUG] handleAdminCreateStory: error levelRequired out of range levelRequired=%d", levelRequired)
		httpx.Error(w, http.StatusBadRequest, "bad_request", storyLevelRangeMessage)
		return
	}
	createdBy := uuid.NullUUID{}
	if uid := httpx.UserIDFrom(r.Context()); uid != uuid.Nil {
		createdBy = uuid.NullUUID{UUID: uid, Valid: true}
	}
	story, err := s.store.CreateStory(r.Context(), repository.CreateStoryParams{
		Title:         r.FormValue("title"),
		Category:      r.FormValue("category"),
		Author:        r.FormValue("author"),
		Description:   r.FormValue("description"),
		LevelRequired: levelRequired,
		CoverImage:    uuid.NullUUID{UUID: coverID, Valid: true},
		CreatedBy:     createdBy,
	})
	if err != nil {
		log.Printf("[DEBUG] handleAdminCreateStory: error CreateStory: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create story")
		return
	}
	log.Printf("[DEBUG] handleAdminCreateStory: success storyID=%s", story.ID)
	httpx.JSON(w, http.StatusCreated, story)
}

// adminStoryConfigRequest is the editable, non-content configuration of a
// story. Kept separate from the multipart create/page endpoints because it
// carries no image.
type adminStoryConfigRequest struct {
	LevelRequired int `json:"levelRequired"`
}

// handleAdminUpdateStoryConfig changes a story's level gate. Existing progress
// is untouched: raising the gate stops further play but never revokes pages a
// user already completed, or the XP they earned for them.
//
// @Summary      Update story config (admin)
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  string                   true  "Story id (UUID)"
// @Param        request  body  adminStoryConfigRequest  true  "Story configuration"
// @Success      200  {object}  models.Story
// @Failure      400  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/stories/{id}/config [put]
func (s *Server) handleAdminUpdateStoryConfig(w http.ResponseWriter, r *http.Request) {
	storyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateStoryConfig: error invalid story id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid story id")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateStoryConfig: start storyID=%s adminID=%s", storyID, httpx.UserIDFrom(r.Context()))
	var req adminStoryConfigRequest
	if !decodeJSON(w, r, &req) {
		log.Printf("[DEBUG] handleAdminUpdateStoryConfig: error decoding request body storyID=%s", storyID)
		return
	}
	if !validStoryLevel(req.LevelRequired) {
		log.Printf("[DEBUG] handleAdminUpdateStoryConfig: error levelRequired out of range levelRequired=%d", req.LevelRequired)
		httpx.Error(w, http.StatusBadRequest, "bad_request", storyLevelRangeMessage)
		return
	}

	err = s.store.UpdateStoryLevelRequired(r.Context(), storyID, req.LevelRequired)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleAdminUpdateStoryConfig: story not found storyID=%s", storyID)
		httpx.Error(w, http.StatusNotFound, "not_found", "story not found")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateStoryConfig: error UpdateStoryLevelRequired storyID=%s: %v", storyID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update story")
		return
	}

	// Returned as an admin view (uuid.Nil), so progress fields stay zeroed and
	// the story reads as unlocked regardless of the new gate.
	detail, err := s.store.GetStory(r.Context(), storyID, uuid.Nil)
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateStoryConfig: error GetStory storyID=%s: %v", storyID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load story")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateStoryConfig: success storyID=%s levelRequired=%d", storyID, req.LevelRequired)
	httpx.JSON(w, http.StatusOK, detail.Story)
}

// handleAdminAddStoryPage uploads a page image and appends it to a story.
//
// @Summary      Add story page (admin)
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id           path      string  true   "Story id (UUID)"
// @Param        file         formData  file    true   "Page image (max 15MB)"
// @Param        title        formData  string  false  "Chapter/page title"
// @Param        description  formData  string  false  "Narrative text"
// @Param        gridCols     formData  int     false  "Grid columns (default 4)"
// @Param        gridRows     formData  int     false  "Grid rows (default 6)"
// @Param        playSeconds  formData  int     false  "Time limit in seconds (default 300)"
// @Param        xpReward     formData  int     false  "XP awarded (default 100)"
// @Success      201  {object}  models.StoryPage
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/stories/{id}/pages [post]
func (s *Server) handleAdminAddStoryPage(w http.ResponseWriter, r *http.Request) {
	storyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminAddStoryPage: error invalid story id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid story id")
		return
	}
	log.Printf("[DEBUG] handleAdminAddStoryPage: start storyID=%s adminID=%s", storyID, httpx.UserIDFrom(r.Context()))
	if exists, e := s.store.StoryExists(r.Context(), storyID); e == nil && !exists {
		log.Printf("[DEBUG] handleAdminAddStoryPage: story not found storyID=%s", storyID)
		httpx.Error(w, http.StatusNotFound, "not_found", "story not found")
		return
	}

	imageID, ok := s.resolveImageID(w, r, false)
	if !ok {
		log.Printf("[DEBUG] handleAdminAddStoryPage: resolveImageID failed storyID=%s", storyID)
		return
	}
	gridCols := atoiDefault(r.FormValue("gridCols"), 4)
	gridRows := atoiDefault(r.FormValue("gridRows"), 6)
	if gridCols <= 0 || gridRows <= 0 {
		log.Printf("[DEBUG] handleAdminAddStoryPage: error invalid grid gridCols=%d gridRows=%d", gridCols, gridRows)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "gridCols/gridRows must be positive")
		return
	}
	page, err := s.store.AddStoryPage(r.Context(), repository.AddStoryPageParams{
		StoryID:     storyID,
		ImageID:     imageID,
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		GridCols:    gridCols,
		GridRows:    gridRows,
		PlaySeconds: atoiDefault(r.FormValue("playSeconds"), 300),
		XPReward:    atoiDefault(r.FormValue("xpReward"), 100),
	})
	if err != nil {
		log.Printf("[DEBUG] handleAdminAddStoryPage: error AddStoryPage storyID=%s: %v", storyID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not add page")
		return
	}
	log.Printf("[DEBUG] handleAdminAddStoryPage: success storyID=%s pageID=%s", storyID, page.ID)
	httpx.JSON(w, http.StatusCreated, page)
}

// handleAdminUpdateStoryPage edits a page's title/description and (optionally)
// its image. Grid size, time limit and XP reward cannot be changed.
//
// @Summary      Update story page (admin)
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id           path      string  true   "Story id (UUID)"
// @Param        pageId       path      string  true   "Page id (UUID)"
// @Param        file         formData  file    false  "New image (optional)"
// @Param        title        formData  string  false  "Chapter/page title"
// @Param        description  formData  string  false  "Narrative text"
// @Success      200  {object}  map[string]bool
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/admin/stories/{id}/pages/{pageId} [put]
func (s *Server) handleAdminUpdateStoryPage(w http.ResponseWriter, r *http.Request) {
	storyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateStoryPage: error invalid story id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid story id")
		return
	}
	pageID, err := uuid.Parse(r.PathValue("pageId"))
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateStoryPage: error invalid page id %q: %v", r.PathValue("pageId"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid page id")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateStoryPage: start storyID=%s pageID=%s adminID=%s", storyID, pageID, httpx.UserIDFrom(r.Context()))
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		log.Printf("[DEBUG] handleAdminUpdateStoryPage: error ParseMultipartForm: %v", err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid multipart form")
		return
	}

	// Optional new image (library imageId or uploaded file).
	imageID, ok := s.optionalImageID(w, r, false)
	if !ok {
		log.Printf("[DEBUG] handleAdminUpdateStoryPage: optionalImageID failed pageID=%s", pageID)
		return
	}

	err = s.store.UpdateStoryPage(r.Context(), pageID, storyID,
		r.FormValue("title"), r.FormValue("description"), imageID)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleAdminUpdateStoryPage: story page not found storyID=%s pageID=%s", storyID, pageID)
		httpx.Error(w, http.StatusNotFound, "not_found", "story page not found")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleAdminUpdateStoryPage: error UpdateStoryPage pageID=%s: %v", pageID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update page")
		return
	}
	log.Printf("[DEBUG] handleAdminUpdateStoryPage: success storyID=%s pageID=%s", storyID, pageID)
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
