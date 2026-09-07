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

type storiesResponse struct {
	Stories []models.Story `json:"stories"`
	pageMeta
}

// storyCompleteResponse is returned after completing a story page: the XP
// awarded and the user's resulting level progress (for the win screen).
type storyCompleteResponse struct {
	Completed      bool `json:"completed"`
	XPEarned       int  `json:"xpEarned"`
	XP             int  `json:"xp"`
	Level          int  `json:"level"`
	XPIntoLevel    int  `json:"xpIntoLevel"`
	XPForNextLevel int  `json:"xpForNextLevel"`
}

// handleListStories lists all stories with the caller's progress.
//
// @Summary      List stories
// @Tags         stories
// @Produce      json
// @Security     BearerAuth
// @Param        page   query  int  false  "Page number (1-based); needs limit or defaults to 8"
// @Param        limit  query  int  false  "Rows per page; omit for the whole list"
// @Success      200  {object}  storiesResponse
// @Router       /api/v1/stories [get]
func (s *Server) handleListStories(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFrom(r.Context())
	page := httpx.ParsePage(r, maxPageLimit)
	log.Printf("[DEBUG] handleListStories: start userID=%s page=%d limit=%d", uid, page.Number, page.Limit)
	list, total, err := s.store.ListStories(r.Context(), uid, page.Limit, page.Offset)
	if err != nil {
		log.Printf("[DEBUG] handleListStories: error ListStories userID=%s: %v", uid, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load stories")
		return
	}
	log.Printf("[DEBUG] handleListStories: success userID=%s count=%d total=%d", uid, len(list), total)
	httpx.JSON(w, http.StatusOK, storiesResponse{Stories: s.decorateStories(list), pageMeta: metaFor(page, total)})
}

// handleGetStory returns a story with its ordered pages and unlock state.
//
// @Summary      Get story
// @Tags         stories
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Story id (UUID)"
// @Success      200  {object}  models.StoryDetail
// @Failure      404  {object}  httpx.ErrorBody
// @Router       /api/v1/stories/{id} [get]
func (s *Server) handleGetStory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleGetStory: error invalid story id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid story id")
		return
	}
	ctx := r.Context()
	uid := httpx.UserIDFrom(ctx)
	log.Printf("[DEBUG] handleGetStory: start userID=%s storyID=%s", uid, id)
	d, err := s.store.GetStory(ctx, id, uid)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleGetStory: story not found storyID=%s", id)
		httpx.Error(w, http.StatusNotFound, "not_found", "story not found")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleGetStory: error GetStory storyID=%s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load story")
		return
	}

	// The level gate withholds the content itself, not just the ability to
	// finish it. Each page carries its chapter image URL, and those are served
	// by the CDN without auth — handing them to an under-level user gives away
	// exactly what the gate exists to hold back. Title, cover and the page
	// counts stay, so the client can still show the lock and what is behind it.
	//
	// Admins manage a story's pages through this same endpoint (the admin page
	// editor reads it), so they are exempt regardless of their own level.
	if !d.Unlocked && httpx.RoleFrom(ctx) != models.RoleAdmin {
		log.Printf("[DEBUG] handleGetStory: withholding pages, level gate userID=%s storyID=%s levelRequired=%d",
			uid, id, d.LevelRequired)
		d.Pages = []models.StoryPage{}
	}

	log.Printf("[DEBUG] handleGetStory: success userID=%s storyID=%s unlocked=%t", uid, id, d.Unlocked)
	httpx.JSON(w, http.StatusOK, d)
}

// handleCompleteStoryPage marks a story page completed (unlocking the next) and
// awards its XP the first time.
//
// @Summary      Complete story page
// @Tags         stories
// @Produce      json
// @Security     BearerAuth
// @Param        id      path  string  true  "Story id (UUID)"
// @Param        pageId  path  string  true  "Page id (UUID)"
// @Success      200  {object}  storyCompleteResponse
// @Failure      403  {object}  httpx.ErrorBody
// @Failure      404  {object}  httpx.ErrorBody
// @Failure      409  {object}  httpx.ErrorBody
// @Router       /api/v1/stories/{id}/pages/{pageId}/complete [post]
func (s *Server) handleCompleteStoryPage(w http.ResponseWriter, r *http.Request) {
	storyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		log.Printf("[DEBUG] handleCompleteStoryPage: error invalid story id %q: %v", r.PathValue("id"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid story id")
		return
	}
	pageID, err := uuid.Parse(r.PathValue("pageId"))
	if err != nil {
		log.Printf("[DEBUG] handleCompleteStoryPage: error invalid page id %q: %v", r.PathValue("pageId"), err)
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid page id")
		return
	}
	uid := httpx.UserIDFrom(r.Context())
	log.Printf("[DEBUG] handleCompleteStoryPage: start userID=%s storyID=%s pageID=%s", uid, storyID, pageID)

	awarded, newly, err := s.store.CompleteStoryPage(r.Context(), uid, storyID, pageID)
	if errors.Is(err, repository.ErrNotFound) {
		log.Printf("[DEBUG] handleCompleteStoryPage: story page not found storyID=%s pageID=%s", storyID, pageID)
		httpx.Error(w, http.StatusNotFound, "not_found", "story page not found")
		return
	}
	if errors.Is(err, repository.ErrLevelTooLow) {
		log.Printf("[DEBUG] handleCompleteStoryPage: level too low userID=%s storyID=%s", uid, storyID)
		httpx.Error(w, http.StatusForbidden, "level_too_low", "your level is too low for this story")
		return
	}
	if errors.Is(err, repository.ErrLocked) {
		log.Printf("[DEBUG] handleCompleteStoryPage: page locked storyID=%s pageID=%s", storyID, pageID)
		httpx.Error(w, http.StatusConflict, "locked", "complete the previous page first")
		return
	}
	if err != nil {
		log.Printf("[DEBUG] handleCompleteStoryPage: error CompleteStoryPage userID=%s pageID=%s: %v", uid, pageID, err)
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not complete page")
		return
	}

	resp := storyCompleteResponse{Completed: true}
	total := 0
	if newly && awarded > 0 {
		if t, e := s.store.AddXP(r.Context(), uid, awarded); e == nil {
			total = t
			resp.XPEarned = awarded
			log.Printf("[DEBUG] handleCompleteStoryPage: XP awarded userID=%s pageID=%s xpEarned=%d totalXP=%d", uid, pageID, awarded, total)
		} else {
			log.Printf("[DEBUG] handleCompleteStoryPage: error AddXP userID=%s: %v", uid, e)
		}
	} else {
		total, _ = s.store.GetXP(r.Context(), uid)
	}
	level, into, next := models.LevelFromXP(total)
	resp.XP = total
	resp.Level = level
	resp.XPIntoLevel = into
	resp.XPForNextLevel = next
	metrics.GameEventsTotal.WithLabelValues("story", "complete").Inc()
	log.Printf("[DEBUG] handleCompleteStoryPage: success userID=%s storyID=%s pageID=%s newly=%t", uid, storyID, pageID, newly)
	httpx.JSON(w, http.StatusOK, resp)
}
