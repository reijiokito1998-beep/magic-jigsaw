package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/reijiokito/jigsaw-backend/internal/models"
)

// storyViewerLevel is the level used to decide which stories are unlocked for
// this caller. uuid.Nil means an admin listing rather than a player, so it gets
// the top level and sees everything unlocked.
func (s *Store) storyViewerLevel(ctx context.Context, userID uuid.UUID) (int, error) {
	if userID == uuid.Nil {
		return models.MaxLevel, nil
	}
	xp, err := s.GetXP(ctx, userID)
	if err != nil {
		return 0, err
	}
	level, _, _ := models.LevelFromXP(xp)
	return level, nil
}

// ListStories returns all stories with per-user progress and unlock state. Pass
// uuid.Nil for userID (e.g. admin listing) to get zeroed progress and every
// story reported as unlocked.
func (s *Store) ListStories(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.Story, int, error) {
	const q = `
SELECT st.id, st.title, st.category, st.author, st.description,
       COALESCE(ci.url, ''), st.level_required, st.created_at,
       (SELECT count(*) FROM story_pages sp WHERE sp.story_id = st.id),
       (SELECT count(*) FROM story_pages sp
          JOIN story_page_progress spp ON spp.page_id = sp.id AND spp.user_id = $1
          WHERE sp.story_id = st.id),
       COALESCE((SELECT sum(sp.xp_reward) FROM story_pages sp
          JOIN story_page_progress spp ON spp.page_id = sp.id AND spp.user_id = $1
          WHERE sp.story_id = st.id), 0),
       count(*) OVER ()
FROM stories st
LEFT JOIN images ci ON ci.id = st.cover_image_id
ORDER BY st.created_at ASC
LIMIT NULLIF($2, 0) OFFSET $3`

	log.Printf("[DEBUG] ListStories: userID=%v limit=%d offset=%d", userID, limit, offset)
	viewerLevel, err := s.storyViewerLevel(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] ListStories: query failed: %v", err)
		return nil, 0, fmt.Errorf("list stories: %w", err)
	}
	defer rows.Close()

	out := make([]models.Story, 0)
	total := 0
	for rows.Next() {
		var st models.Story
		if err := rows.Scan(&st.ID, &st.Title, &st.Category, &st.Author, &st.Description,
			&st.CoverURL, &st.LevelRequired, &st.CreatedAt,
			&st.TotalPages, &st.CompletedPages, &st.EarnedXP, &total); err != nil {
			log.Printf("[DEBUG] ListStories: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan story: %w", err)
		}
		st.Unlocked = viewerLevel >= st.LevelRequired
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListStories: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] ListStories: returned %d of %d stories", len(out), total)
	return out, total, nil
}

// GetStory returns a story with its ordered pages and per-user unlock state.
func (s *Store) GetStory(ctx context.Context, id, userID uuid.UUID) (models.StoryDetail, error) {
	log.Printf("[DEBUG] GetStory: id=%v userID=%v", id, userID)
	var d models.StoryDetail
	err := s.pool.QueryRow(ctx, `
SELECT st.id, st.title, st.category, st.author, st.description, COALESCE(ci.url, ''),
       st.level_required, st.created_at
FROM stories st
LEFT JOIN images ci ON ci.id = st.cover_image_id
WHERE st.id = $1`, id).Scan(
		&d.ID, &d.Title, &d.Category, &d.Author, &d.Description, &d.CoverURL,
		&d.LevelRequired, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetStory: not found id=%v", id)
		return d, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] GetStory: query failed: %v", err)
		return d, fmt.Errorf("get story: %w", err)
	}

	viewerLevel, err := s.storyViewerLevel(ctx, userID)
	if err != nil {
		return d, err
	}
	d.Unlocked = viewerLevel >= d.LevelRequired

	rows, err := s.pool.Query(ctx, `
SELECT sp.id, sp.story_id, sp.page_index, sp.title, sp.description, sp.grid_cols, sp.grid_rows, sp.play_seconds, sp.xp_reward,
       i.id, i.public_id, i.url, i.width, i.height, i.title, i.uploaded_by, i.is_challenge, i.created_at,
       (spp.page_id IS NOT NULL)
FROM story_pages sp
JOIN images i ON i.id = sp.image_id
LEFT JOIN story_page_progress spp ON spp.page_id = sp.id AND spp.user_id = $2
WHERE sp.story_id = $1
ORDER BY sp.page_index ASC`, id, userID)
	if err != nil {
		log.Printf("[DEBUG] GetStory: pages query failed: %v", err)
		return d, fmt.Errorf("get story pages: %w", err)
	}
	defer rows.Close()

	pages := make([]models.StoryPage, 0)
	for rows.Next() {
		var p models.StoryPage
		var img models.Image
		if err := rows.Scan(&p.ID, &p.StoryID, &p.Index, &p.Title, &p.Description, &p.GridCols, &p.GridRows, &p.PlaySeconds, &p.XPReward,
			&img.ID, &img.PublicID, &img.URL, &img.Width, &img.Height, &img.Title, &img.UploadedBy, &img.IsChallenge, &img.CreatedAt,
			&p.Completed); err != nil {
			log.Printf("[DEBUG] GetStory: scan page failed: %v", err)
			return d, fmt.Errorf("scan story page: %w", err)
		}
		p.Image = img
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] GetStory: rows error: %v", err)
		return d, err
	}

	// Sequential unlock: page 0 is always unlocked; page k unlocks once page
	// k-1 is completed. A story gated above the user's level locks every page
	// regardless, so these flags can never tell a client that a page is
	// playable while the story itself is not.
	//
	// Counts below are still taken from the full page list: a user who was
	// mid-story when an admin raised the gate keeps the pages and XP they had
	// already earned.
	for i := range pages {
		switch {
		case !d.Unlocked:
			pages[i].Unlocked = false
		case i == 0:
			pages[i].Unlocked = true
		default:
			pages[i].Unlocked = pages[i-1].Completed
		}
		if pages[i].Completed {
			d.CompletedPages++
			d.EarnedXP += pages[i].XPReward
		}
	}
	d.TotalPages = len(pages)
	d.Pages = pages
	log.Printf("[DEBUG] GetStory: id=%v totalPages=%d completedPages=%d earnedXP=%d",
		id, d.TotalPages, d.CompletedPages, d.EarnedXP)
	return d, nil
}

// CompleteStoryPage marks a page completed for the user (idempotent). It fails
// with ErrLevelTooLow when the story is gated above the user's level, and with
// ErrLocked if the previous page isn't completed yet. Returns the XP to award
// (0 if the page was already completed) and whether it was newly done.
func (s *Store) CompleteStoryPage(ctx context.Context, userID, storyID, pageID uuid.UUID) (awardedXP int, newly bool, err error) {
	log.Printf("[DEBUG] CompleteStoryPage: userID=%v storyID=%v pageID=%v", userID, storyID, pageID)
	var idx, xp, levelRequired int
	err = s.pool.QueryRow(ctx, `
SELECT sp.page_index, sp.xp_reward, st.level_required
FROM story_pages sp
JOIN stories st ON st.id = sp.story_id
WHERE sp.id = $1 AND sp.story_id = $2`,
		pageID, storyID).Scan(&idx, &xp, &levelRequired)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] CompleteStoryPage: page not found pageID=%v storyID=%v", pageID, storyID)
		return 0, false, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] CompleteStoryPage: load page failed: %v", err)
		return 0, false, fmt.Errorf("load story page: %w", err)
	}

	// Re-checked here rather than trusted from the client: the level gate is
	// only advisory in the UI, this is what actually enforces it.
	viewerLevel, err := s.storyViewerLevel(ctx, userID)
	if err != nil {
		return 0, false, err
	}
	if viewerLevel < levelRequired {
		log.Printf("[DEBUG] CompleteStoryPage: level too low userID=%v level=%d required=%d",
			userID, viewerLevel, levelRequired)
		return 0, false, ErrLevelTooLow
	}

	if idx > 0 {
		var prevDone bool
		if err = s.pool.QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1 FROM story_pages sp
  JOIN story_page_progress spp ON spp.page_id = sp.id AND spp.user_id = $1
  WHERE sp.story_id = $2 AND sp.page_index = $3)`,
			userID, storyID, idx-1).Scan(&prevDone); err != nil {
			log.Printf("[DEBUG] CompleteStoryPage: check prev page failed: %v", err)
			return 0, false, fmt.Errorf("check prev page: %w", err)
		}
		if !prevDone {
			log.Printf("[DEBUG] CompleteStoryPage: locked, previous page %d not done", idx-1)
			return 0, false, ErrLocked
		}
	}

	tag, e := s.pool.Exec(ctx,
		`INSERT INTO story_page_progress (user_id, page_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, pageID)
	if e != nil {
		log.Printf("[DEBUG] CompleteStoryPage: mark page failed: %v", e)
		return 0, false, fmt.Errorf("mark story page: %w", e)
	}
	newly = tag.RowsAffected() > 0
	if newly {
		awardedXP = xp
	}
	log.Printf("[DEBUG] CompleteStoryPage: userID=%v pageID=%v newly=%v awardedXP=%d", userID, pageID, newly, awardedXP)
	return awardedXP, newly, nil
}

// --- Admin ------------------------------------------------------------------

// CreateStoryParams describes a new story (cover image already uploaded).
type CreateStoryParams struct {
	Title       string
	Category    string
	Author      string
	Description string
	// LevelRequired gates the story behind a player level; 1 is open to all.
	LevelRequired int
	CoverImage    uuid.NullUUID
	CreatedBy     uuid.NullUUID
}

// CreateStory inserts a story and returns it.
func (s *Store) CreateStory(ctx context.Context, p CreateStoryParams) (models.Story, error) {
	log.Printf("[DEBUG] CreateStory: title=%s category=%s levelRequired=%d", p.Title, p.Category, p.LevelRequired)
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
INSERT INTO stories (title, category, author, description, level_required, cover_image_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id`, p.Title, p.Category, p.Author, p.Description, p.LevelRequired,
		p.CoverImage, p.CreatedBy).Scan(&id)
	if err != nil {
		log.Printf("[DEBUG] CreateStory: query failed: %v", err)
		return models.Story{}, fmt.Errorf("create story: %w", err)
	}
	log.Printf("[DEBUG] CreateStory: created story id=%v", id)
	// Unpaginated on purpose: the freshly created story has to be found
	// wherever it sorted into the list.
	list, _, err := s.ListStories(ctx, uuid.Nil, 0, 0)
	if err != nil {
		return models.Story{}, err
	}
	for _, st := range list {
		if st.ID == id {
			return st, nil
		}
	}
	return models.Story{ID: id, Title: p.Title}, nil
}

// AddStoryPageParams describes a new page appended to a story.
type AddStoryPageParams struct {
	StoryID     uuid.UUID
	ImageID     uuid.UUID
	Title       string
	Description string
	GridCols    int
	GridRows    int
	PlaySeconds int
	XPReward    int
}

// AddStoryPage appends a page at the next index and returns it.
func (s *Store) AddStoryPage(ctx context.Context, p AddStoryPageParams) (models.StoryPage, error) {
	log.Printf("[DEBUG] AddStoryPage: storyID=%v title=%s", p.StoryID, p.Title)
	var id uuid.UUID
	var idx int
	err := s.pool.QueryRow(ctx, `
INSERT INTO story_pages (story_id, page_index, image_id, title, description, grid_cols, grid_rows, play_seconds, xp_reward)
VALUES (
  $1,
  COALESCE((SELECT max(page_index) + 1 FROM story_pages WHERE story_id = $1), 0),
  $2, $3, $4, $5, $6, $7, $8)
RETURNING id, page_index`,
		p.StoryID, p.ImageID, p.Title, p.Description, p.GridCols, p.GridRows, p.PlaySeconds, p.XPReward).Scan(&id, &idx)
	if err != nil {
		log.Printf("[DEBUG] AddStoryPage: query failed: %v", err)
		return models.StoryPage{}, fmt.Errorf("add story page: %w", err)
	}
	log.Printf("[DEBUG] AddStoryPage: created page id=%v index=%d", id, idx)
	return models.StoryPage{
		ID: id, StoryID: p.StoryID, Index: idx, Title: p.Title, Description: p.Description,
		GridCols: p.GridCols, GridRows: p.GridRows, PlaySeconds: p.PlaySeconds, XPReward: p.XPReward,
	}, nil
}

// UpdateStoryPage edits a page's title/description and (optionally) its image.
// Grid size, time limit and XP reward are intentionally NOT changed here.
func (s *Store) UpdateStoryPage(ctx context.Context, pageID, storyID uuid.UUID, title, description string, imageID uuid.NullUUID) error {
	const q = `
UPDATE story_pages
SET title = $3, description = $4, image_id = COALESCE($5, image_id)
WHERE id = $1 AND story_id = $2
RETURNING id`
	log.Printf("[DEBUG] UpdateStoryPage: pageID=%v storyID=%v", pageID, storyID)
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, q, pageID, storyID, title, description, imageID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] UpdateStoryPage: not found pageID=%v storyID=%v", pageID, storyID)
		return ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] UpdateStoryPage: query failed: %v", err)
		return fmt.Errorf("update story page: %w", err)
	}
	return nil
}

// UpdateStoryLevelRequired changes the level gate of an existing story.
func (s *Store) UpdateStoryLevelRequired(ctx context.Context, storyID uuid.UUID, levelRequired int) error {
	const q = `UPDATE stories SET level_required = $2 WHERE id = $1 RETURNING id`
	log.Printf("[DEBUG] UpdateStoryLevelRequired: storyID=%v levelRequired=%d", storyID, levelRequired)
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, q, storyID, levelRequired).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] UpdateStoryLevelRequired: not found storyID=%v", storyID)
		return ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] UpdateStoryLevelRequired: query failed: %v", err)
		return fmt.Errorf("update story level: %w", err)
	}
	return nil
}

// StoryExists reports whether a story id exists.
func (s *Store) StoryExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stories WHERE id = $1)`, id).Scan(&ok); err != nil {
		log.Printf("[DEBUG] StoryExists: query failed: %v", err)
		return false, fmt.Errorf("story exists: %w", err)
	}
	return ok, nil
}
