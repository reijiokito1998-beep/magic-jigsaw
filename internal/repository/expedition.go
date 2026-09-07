package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/models"
)

// ExpeditionProgressRow is a user's progress on one level.
type ExpeditionProgressRow struct {
	LevelIndex     int
	Status         string
	ElapsedSeconds int
	CorrectPieces  int
}

// SetLevelImage assigns (or replaces) the image for an expedition level.
func (s *Store) SetLevelImage(ctx context.Context, levelIndex int, imageID uuid.UUID) error {
	const q = `
INSERT INTO expedition_levels (level_index, image_id, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (level_index) DO UPDATE SET image_id = EXCLUDED.image_id, updated_at = now()`
	log.Printf("[DEBUG] SetLevelImage: levelIndex=%d imageID=%v", levelIndex, imageID)
	if _, err := s.pool.Exec(ctx, q, levelIndex, imageID); err != nil {
		log.Printf("[DEBUG] SetLevelImage: query failed: %v", err)
		return fmt.Errorf("set level image: %w", err)
	}
	return nil
}

// LevelImage is a level's assigned image (URL + server id).
type LevelImage struct {
	URL string
	ID  string
}

// LevelImages returns a map of level index → image (only levels that have an
// image assigned). The id lets clients favourite the image by reference.
func (s *Store) LevelImages(ctx context.Context) (map[int]LevelImage, error) {
	const q = `
SELECT el.level_index, i.url, i.id
FROM expedition_levels el
JOIN images i ON i.id = el.image_id`
	log.Printf("[DEBUG] LevelImages: querying")
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		log.Printf("[DEBUG] LevelImages: query failed: %v", err)
		return nil, fmt.Errorf("level images: %w", err)
	}
	defer rows.Close()

	out := map[int]LevelImage{}
	for rows.Next() {
		var idx int
		var url string
		var id uuid.UUID
		if err := rows.Scan(&idx, &url, &id); err != nil {
			log.Printf("[DEBUG] LevelImages: scan failed: %v", err)
			return nil, fmt.Errorf("scan level image: %w", err)
		}
		out[idx] = LevelImage{URL: url, ID: id.String()}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] LevelImages: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] LevelImages: returned %d entries", len(out))
	return out, nil
}

// LevelConfigs returns admin config overrides keyed by level index. Only the
// columns an admin has set are non-nil; the rest fall back to tier defaults.
func (s *Store) LevelConfigs(ctx context.Context) (map[int]*models.LevelConfig, error) {
	const q = `
SELECT level_index, star, grid_cols, grid_rows, play_seconds, category, xp_reward, show_on_main
FROM expedition_levels`
	log.Printf("[DEBUG] LevelConfigs: querying")
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		log.Printf("[DEBUG] LevelConfigs: query failed: %v", err)
		return nil, fmt.Errorf("level configs: %w", err)
	}
	defer rows.Close()

	out := map[int]*models.LevelConfig{}
	for rows.Next() {
		var idx int
		var c models.LevelConfig
		if err := rows.Scan(&idx, &c.Star, &c.GridCols, &c.GridRows, &c.PlaySeconds, &c.Category, &c.XPReward, &c.ShowOnMain); err != nil {
			log.Printf("[DEBUG] LevelConfigs: scan failed: %v", err)
			return nil, fmt.Errorf("scan level config: %w", err)
		}
		cfg := c
		out[idx] = &cfg
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] LevelConfigs: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] LevelConfigs: returned %d configs", len(out))
	return out, nil
}

// ExpeditionLevelCount is how many levels the campaign currently has. Levels
// are numbered 1..N with no gaps (they are only ever appended or removed from
// the end), so the highest index is the count.
func (s *Store) ExpeditionLevelCount(ctx context.Context) (int, error) {
	const q = `SELECT COALESCE(MAX(level_index), 0) FROM expedition_levels`
	var n int
	if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		log.Printf("[DEBUG] ExpeditionLevelCount: query failed: %v", err)
		return 0, fmt.Errorf("expedition level count: %w", err)
	}
	return n, nil
}

// CreateExpeditionLevel appends an empty level (no image, no config override)
// at the end of the campaign and returns its index. The index is derived
// inside the statement so two admins adding at once cannot pick the same one:
// the primary key on level_index rejects the loser instead of overwriting.
func (s *Store) CreateExpeditionLevel(ctx context.Context) (int, error) {
	const q = `
INSERT INTO expedition_levels (level_index, updated_at)
SELECT COALESCE(MAX(level_index), 0) + 1, now() FROM expedition_levels
RETURNING level_index`
	var idx int
	if err := s.pool.QueryRow(ctx, q).Scan(&idx); err != nil {
		log.Printf("[DEBUG] CreateExpeditionLevel: query failed: %v", err)
		return 0, fmt.Errorf("create expedition level: %w", err)
	}
	log.Printf("[DEBUG] CreateExpeditionLevel: created levelIndex=%d", idx)
	return idx, nil
}

// DeleteLastExpeditionLevel removes level levelIndex, but only when it is the
// last one and no user has played it. Deleting anywhere else would leave a gap
// in the 1..N numbering, and deleting a played level would silently take stars
// away from whoever completed it — both are refused rather than repaired.
func (s *Store) DeleteLastExpeditionLevel(ctx context.Context, levelIndex int) (deleted bool, reason string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] DeleteLastExpeditionLevel: begin tx failed: %v", err)
		return false, "", fmt.Errorf("begin delete level tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var maxIndex int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(level_index), 0) FROM expedition_levels`).Scan(&maxIndex); err != nil {
		log.Printf("[DEBUG] DeleteLastExpeditionLevel: max index query failed: %v", err)
		return false, "", fmt.Errorf("max level index: %w", err)
	}
	if levelIndex != maxIndex {
		return false, "only the last level can be deleted", nil
	}

	var played int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM expedition_progress WHERE level_index = $1`, levelIndex).Scan(&played); err != nil {
		log.Printf("[DEBUG] DeleteLastExpeditionLevel: progress count failed: %v", err)
		return false, "", fmt.Errorf("count level progress: %w", err)
	}
	if played > 0 {
		return false, "players have already started this level", nil
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM expedition_levels WHERE level_index = $1`, levelIndex); err != nil {
		log.Printf("[DEBUG] DeleteLastExpeditionLevel: delete failed: %v", err)
		return false, "", fmt.Errorf("delete expedition level: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] DeleteLastExpeditionLevel: commit failed: %v", err)
		return false, "", fmt.Errorf("commit delete level: %w", err)
	}
	log.Printf("[DEBUG] DeleteLastExpeditionLevel: deleted levelIndex=%d", levelIndex)
	return true, "", nil
}

// EffectiveTiers returns the effective (admin-override applied) tier for every
// level 1..ExpeditionLevelCount.
func (s *Store) EffectiveTiers(ctx context.Context) (map[int]models.ExpeditionTier, error) {
	count, err := s.ExpeditionLevelCount(ctx)
	if err != nil {
		return nil, err
	}
	cfgs, err := s.LevelConfigs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int]models.ExpeditionTier, count)
	for i := 1; i <= count; i++ {
		out[i] = models.EffectiveTier(i, cfgs[i])
	}
	log.Printf("[DEBUG] EffectiveTiers: computed %d tiers", len(out))
	return out, nil
}

// UpdateLevelConfig upserts a level's admin config override (all fields).
func (s *Store) UpdateLevelConfig(ctx context.Context, levelIndex int, c models.LevelConfig) error {
	const q = `
INSERT INTO expedition_levels (level_index, star, grid_cols, grid_rows, play_seconds, category, xp_reward, show_on_main, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, TRUE), now())
ON CONFLICT (level_index) DO UPDATE SET
    star = EXCLUDED.star,
    grid_cols = EXCLUDED.grid_cols,
    grid_rows = EXCLUDED.grid_rows,
    play_seconds = EXCLUDED.play_seconds,
    category = EXCLUDED.category,
    xp_reward = EXCLUDED.xp_reward,
    show_on_main = EXCLUDED.show_on_main,
    updated_at = now()`
	log.Printf("[DEBUG] UpdateLevelConfig: levelIndex=%d", levelIndex)
	if _, err := s.pool.Exec(ctx, q, levelIndex, c.Star, c.GridCols, c.GridRows, c.PlaySeconds, c.Category, c.XPReward, c.ShowOnMain); err != nil {
		log.Printf("[DEBUG] UpdateLevelConfig: query failed: %v", err)
		return fmt.Errorf("update level config: %w", err)
	}
	return nil
}

// LevelImageID returns the image id assigned to a level (or false).
func (s *Store) LevelImageID(ctx context.Context, levelIndex int) (uuid.UUID, bool, error) {
	const q = `SELECT image_id FROM expedition_levels WHERE level_index = $1`
	var id uuid.NullUUID
	if err := s.pool.QueryRow(ctx, q, levelIndex).Scan(&id); err != nil {
		log.Printf("[DEBUG] LevelImageID: query failed: %v", err)
		return uuid.Nil, false, fmt.Errorf("level image id: %w", err)
	}
	return id.UUID, id.Valid, nil
}

// GetExpeditionProgress returns a user's progress keyed by level index.
func (s *Store) GetExpeditionProgress(ctx context.Context, userID uuid.UUID) (map[int]ExpeditionProgressRow, error) {
	const q = `
SELECT level_index, status, elapsed_seconds, correct_pieces
FROM expedition_progress WHERE user_id = $1`
	log.Printf("[DEBUG] GetExpeditionProgress: userID=%v", userID)
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		log.Printf("[DEBUG] GetExpeditionProgress: query failed: %v", err)
		return nil, fmt.Errorf("expedition progress: %w", err)
	}
	defer rows.Close()

	out := map[int]ExpeditionProgressRow{}
	for rows.Next() {
		var r ExpeditionProgressRow
		if err := rows.Scan(&r.LevelIndex, &r.Status, &r.ElapsedSeconds, &r.CorrectPieces); err != nil {
			log.Printf("[DEBUG] GetExpeditionProgress: scan failed: %v", err)
			return nil, fmt.Errorf("scan progress: %w", err)
		}
		out[r.LevelIndex] = r
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] GetExpeditionProgress: rows error: %v", err)
		return nil, err
	}
	return out, nil
}

// CompletedExpeditionLevels returns the indices of the user's completed levels.
func (s *Store) CompletedExpeditionLevels(ctx context.Context, userID uuid.UUID) ([]int, error) {
	const q = `SELECT level_index FROM expedition_progress WHERE user_id = $1 AND status = 'completed'`
	log.Printf("[DEBUG] CompletedExpeditionLevels: userID=%v", userID)
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		log.Printf("[DEBUG] CompletedExpeditionLevels: query failed: %v", err)
		return nil, fmt.Errorf("completed levels: %w", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var i int
		if err := rows.Scan(&i); err != nil {
			log.Printf("[DEBUG] CompletedExpeditionLevels: scan failed: %v", err)
			return nil, fmt.Errorf("scan completed: %w", err)
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] CompletedExpeditionLevels: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] CompletedExpeditionLevels: userID=%v completed=%d", userID, len(out))
	return out, nil
}

// ExpeditionBonusStars returns the stars a user did not earn from expedition
// levels: purchased stars, plus the daily-challenge stars folded in by
// migration 0021 when daily challenges switched to paying unlock points.
func (s *Store) ExpeditionBonusStars(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `SELECT expedition_bonus_stars FROM users WHERE id = $1`
	var n int
	if err := s.pool.QueryRow(ctx, q, userID).Scan(&n); err != nil {
		log.Printf("[DEBUG] ExpeditionBonusStars: query failed: %v", err)
		return 0, fmt.Errorf("expedition bonus stars: %w", err)
	}
	return n, nil
}

// TotalExpeditionStars = effective tier stars of completed levels + bonus
// stars. Uses admin-edited stars where present.
//
// Daily challenges no longer contribute: completing one pays an unlock point
// instead. The stars players had already earned that way live on in
// expedition_bonus_stars (folded in by migration 0021), so nobody's total drops.
func (s *Store) TotalExpeditionStars(ctx context.Context, userID uuid.UUID) (int, error) {
	completed, err := s.CompletedExpeditionLevels(ctx, userID)
	if err != nil {
		return 0, err
	}
	tiers, err := s.EffectiveTiers(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, idx := range completed {
		// Collection levels count too: they cost no stars to open, but clearing
		// one pays its stars into the same pool, so the collections are a way to
		// get ahead on the map rather than a detour away from it.
		total += tiers[idx].Star
	}
	bonus, err := s.ExpeditionBonusStars(ctx, userID)
	if err != nil {
		return 0, err
	}
	result := total + bonus
	log.Printf("[DEBUG] TotalExpeditionStars: userID=%v levelStars=%d bonusStars=%d total=%d",
		userID, total, bonus, result)
	return result, nil
}

// StartExpeditionLevel marks a level in_progress (unless already completed).
func (s *Store) StartExpeditionLevel(ctx context.Context, userID uuid.UUID, level int) error {
	const q = `
INSERT INTO expedition_progress (user_id, level_index, status)
VALUES ($1, $2, 'in_progress')
ON CONFLICT (user_id, level_index) DO UPDATE
SET status = 'in_progress', started_at = now()
WHERE expedition_progress.status <> 'completed'`
	log.Printf("[DEBUG] StartExpeditionLevel: userID=%v level=%d", userID, level)
	if _, err := s.pool.Exec(ctx, q, userID, level); err != nil {
		log.Printf("[DEBUG] StartExpeditionLevel: query failed: %v", err)
		return fmt.Errorf("start level: %w", err)
	}
	return nil
}

// CompleteExpeditionLevel transitions an in_progress level to completed
// (idempotent). Only a row that is currently in_progress can be completed -
// this prevents completing a level that was never started (or completing an
// already-completed level again) via a direct API call. Returns
// firstCompletion=true only when this call performed that transition (used to
// award XP once).
func (s *Store) CompleteExpeditionLevel(ctx context.Context, userID uuid.UUID, level, elapsedSeconds, correctPieces int) (firstCompletion bool, err error) {
	const q = `
UPDATE expedition_progress
SET status = 'completed', finished_at = now(),
    elapsed_seconds = $3,
    correct_pieces = GREATEST(correct_pieces, $4)
WHERE user_id = $1 AND level_index = $2 AND status = 'in_progress'`
	log.Printf("[DEBUG] CompleteExpeditionLevel: userID=%v level=%d elapsedSeconds=%d correctPieces=%d",
		userID, level, elapsedSeconds, correctPieces)
	tag, err := s.pool.Exec(ctx, q, userID, level, elapsedSeconds, correctPieces)
	if err != nil {
		log.Printf("[DEBUG] CompleteExpeditionLevel: query failed: %v", err)
		return false, fmt.Errorf("complete level: %w", err)
	}
	log.Printf("[DEBUG] CompleteExpeditionLevel: rows affected=%d", tag.RowsAffected())
	return tag.RowsAffected() > 0, nil
}

// FailExpeditionLevel marks an in-progress level failed (retryable).
func (s *Store) FailExpeditionLevel(ctx context.Context, userID uuid.UUID, level, correctPieces int) error {
	const q = `
UPDATE expedition_progress
SET status = 'failed', finished_at = now(),
    correct_pieces = GREATEST(correct_pieces, $3)
WHERE user_id = $1 AND level_index = $2 AND status = 'in_progress'`
	log.Printf("[DEBUG] FailExpeditionLevel: userID=%v level=%d correctPieces=%d", userID, level, correctPieces)
	if _, err := s.pool.Exec(ctx, q, userID, level, correctPieces); err != nil {
		log.Printf("[DEBUG] FailExpeditionLevel: query failed: %v", err)
		return fmt.Errorf("fail level: %w", err)
	}
	return nil
}
