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

// Hidden Secret errors, mapped to HTTP responses at the transport edge.
var (
	// ErrSecretInactive is returned when acting on a hidden secret that is not
	// active.
	ErrSecretInactive = errors.New("hidden secret is inactive")
	// ErrSecretCompleted is returned when acting on a hidden secret the caller
	// has already fully revealed.
	ErrSecretCompleted = errors.New("hidden secret already revealed")
	// ErrTileRevealed is returned when revealing a tile that is already open.
	ErrTileRevealed = errors.New("tile already revealed")
	// ErrTileOutOfRange is returned when a tile index does not address one of
	// the nine shutters.
	ErrTileOutOfRange = errors.New("tile index out of range")
)

// SaveHiddenSecretParams is the admin-authored shape of a hidden secret, used
// for both create and update.
type SaveHiddenSecretParams struct {
	Title           string
	Subtitle        string
	ImageID         uuid.UUID
	XPReward        int
	TileGridCols    int
	TileGridRows    int
	TilePlaySeconds int
	Active          bool
}

// hiddenSecretRow is the raw hidden_secrets row joined to its image URL.
type hiddenSecretRow struct {
	ID              uuid.UUID
	Title           string
	Subtitle        string
	ImageID         uuid.UUID
	ImageURL        string
	XPReward        int
	TileGridCols    int
	TileGridRows    int
	TilePlaySeconds int
	Active          bool
}

func (s *Store) getHiddenSecretRow(ctx context.Context, id uuid.UUID) (hiddenSecretRow, error) {
	var h hiddenSecretRow
	err := s.pool.QueryRow(ctx, `
SELECT h.id, h.title, h.subtitle, h.image_id, COALESCE(i.url, ''),
       h.xp_reward, h.tile_grid_cols, h.tile_grid_rows, h.tile_play_seconds, h.active
FROM hidden_secrets h
JOIN images i ON i.id = h.image_id
WHERE h.id = $1`, id).Scan(
		&h.ID, &h.Title, &h.Subtitle, &h.ImageID, &h.ImageURL,
		&h.XPReward, &h.TileGridCols, &h.TileGridRows, &h.TilePlaySeconds, &h.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] getHiddenSecretRow: secret not found id=%v", id)
		return hiddenSecretRow{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] getHiddenSecretRow: query failed id=%v: %v", id, err)
		return hiddenSecretRow{}, fmt.Errorf("load hidden secret: %w", err)
	}
	return h, nil
}

// ListHiddenSecrets returns every active hidden secret, newest first, with the
// caller's reveal count. Inactive secrets are hidden from players but a secret
// the caller already completed stays listed so the gallery entry keeps its
// place.
//
// The picture itself is withheld until the caller has revealed all nine tiles:
// the whole game mode is the wall between a player and that image, and a list
// row that carried the URL handed it over for free to anyone calling the API
// directly. Both the URL and the image id are withheld — the id alone is enough
// to fetch the picture through POST /api/v1/collection, which accepts any image
// id and then serves its URL back from the collection list.
func (s *Store) ListHiddenSecrets(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.HiddenSecret, int, error) {
	log.Printf("[DEBUG] ListHiddenSecrets: userID=%v limit=%d offset=%d", userID, limit, offset)
	rows, err := s.pool.Query(ctx, `
SELECT h.id, h.title, h.subtitle,
       CASE WHEN COALESCE(p.completed, false)
            THEN h.image_id ELSE '00000000-0000-0000-0000-000000000000'::uuid END,
       CASE WHEN COALESCE(p.completed, false) THEN COALESCE(i.url, '') ELSE '' END,
       h.xp_reward, h.active,
       COALESCE(jsonb_array_length(p.revealed), 0), COALESCE(p.completed, false),
       count(*) OVER ()
FROM hidden_secrets h
JOIN images i ON i.id = h.image_id
LEFT JOIN hidden_secret_progress p ON p.secret_id = h.id AND p.user_id = $1
WHERE h.active
ORDER BY h.created_at DESC
LIMIT NULLIF($2, 0) OFFSET $3`, userID, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] ListHiddenSecrets: query failed: %v", err)
		return nil, 0, fmt.Errorf("list hidden secrets: %w", err)
	}
	defer rows.Close()

	out := make([]models.HiddenSecret, 0)
	total := 0
	for rows.Next() {
		h := models.HiddenSecret{
			TileCount:    models.HiddenSecretTileCount,
			PointsReward: models.HiddenSecretPointsReward,
		}
		if err := rows.Scan(&h.ID, &h.Title, &h.Subtitle, &h.ImageID, &h.ImageURL,
			&h.XPReward, &h.Active, &h.RevealedCount, &h.Completed, &total); err != nil {
			log.Printf("[DEBUG] ListHiddenSecrets: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan hidden secret: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListHiddenSecrets: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] ListHiddenSecrets: userID=%v returned %d of %d secrets", userID, len(out), total)
	return out, total, nil
}

// GetHiddenSecret returns one hidden secret's picture and per-tile puzzle
// settings, plus the caller's revealed tiles.
func (s *Store) GetHiddenSecret(ctx context.Context, id, userID uuid.UUID) (models.HiddenSecretDetail, error) {
	log.Printf("[DEBUG] GetHiddenSecret: id=%v userID=%v", id, userID)
	h, err := s.getHiddenSecretRow(ctx, id)
	if err != nil {
		return models.HiddenSecretDetail{}, err
	}

	progress := models.HiddenSecretProgress{Revealed: []int{}}
	err = s.pool.QueryRow(ctx, `
SELECT revealed, completed FROM hidden_secret_progress
WHERE user_id = $1 AND secret_id = $2`, userID, id).Scan(&progress.Revealed, &progress.Completed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetHiddenSecret: load progress failed: %v", err)
		return models.HiddenSecretDetail{}, fmt.Errorf("load hidden secret progress: %w", err)
	}
	if progress.Revealed == nil {
		progress.Revealed = []int{}
	}

	log.Printf("[DEBUG] GetHiddenSecret: id=%v userID=%v revealed=%d completed=%t",
		id, userID, len(progress.Revealed), progress.Completed)
	return models.HiddenSecretDetail{
		ID: h.ID, Title: h.Title, Subtitle: h.Subtitle,
		ImageID: h.ImageID, ImageURL: h.ImageURL, XPReward: h.XPReward,
		PointsReward: models.HiddenSecretPointsReward,
		TileCols:     models.HiddenSecretTileCols, TileRows: models.HiddenSecretTileRows,
		TileGridCols: h.TileGridCols, TileGridRows: h.TileGridRows,
		TilePlaySeconds: h.TilePlaySeconds,
		Progress:        progress,
	}, nil
}

// RevealHiddenSecretTile opens one shutter for the caller after they solved its
// puzzle. Returns the updated revealed set, whether that was the ninth (and so
// completed the picture) and the XP to award on completion — the caller credits
// the XP, mirroring CompleteDungeon.
//
// The picture is unlocked into the caller's gallery in the same transaction as
// the completing reveal, so a crash can never leave a fully revealed secret
// without its gallery entry.
func (s *Store) RevealHiddenSecretTile(ctx context.Context, userID, secretID uuid.UUID, tileIndex int) (revealed []int, completed bool, xpReward int, err error) {
	log.Printf("[DEBUG] RevealHiddenSecretTile: userID=%v secretID=%v tile=%d", userID, secretID, tileIndex)
	if !models.TileIndexInRange(tileIndex) {
		log.Printf("[DEBUG] RevealHiddenSecretTile: tile index out of range tile=%d", tileIndex)
		return nil, false, 0, ErrTileOutOfRange
	}
	h, err := s.getHiddenSecretRow(ctx, secretID)
	if err != nil {
		return nil, false, 0, err
	}
	if !h.Active {
		log.Printf("[DEBUG] RevealHiddenSecretTile: secret inactive secretID=%v", secretID)
		return nil, false, 0, ErrSecretInactive
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] RevealHiddenSecretTile: begin tx failed: %v", err)
		return nil, false, 0, fmt.Errorf("begin reveal tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Create the progress row on first reveal, then lock it. Doing the insert
	// first means the SELECT ... FOR UPDATE below always finds a row, so two
	// concurrent first-reveals serialize instead of racing.
	if _, err := tx.Exec(ctx, `
INSERT INTO hidden_secret_progress (user_id, secret_id, revealed)
VALUES ($1, $2, '[]'::jsonb)
ON CONFLICT (user_id, secret_id) DO NOTHING`, userID, secretID); err != nil {
		log.Printf("[DEBUG] RevealHiddenSecretTile: ensure progress row failed: %v", err)
		return nil, false, 0, fmt.Errorf("ensure hidden secret progress: %w", err)
	}

	if err := tx.QueryRow(ctx, `
SELECT revealed, completed FROM hidden_secret_progress
WHERE user_id = $1 AND secret_id = $2 FOR UPDATE`, userID, secretID).Scan(&revealed, &completed); err != nil {
		log.Printf("[DEBUG] RevealHiddenSecretTile: load progress failed: %v", err)
		return nil, false, 0, fmt.Errorf("load hidden secret progress: %w", err)
	}
	if completed {
		log.Printf("[DEBUG] RevealHiddenSecretTile: secretID=%v already completed by userID=%v", secretID, userID)
		return nil, false, 0, ErrSecretCompleted
	}
	for _, t := range revealed {
		if t == tileIndex {
			log.Printf("[DEBUG] RevealHiddenSecretTile: tile=%d already revealed", tileIndex)
			return nil, false, 0, ErrTileRevealed
		}
	}
	revealed = append(revealed, tileIndex)
	completed = len(revealed) >= models.HiddenSecretTileCount

	if _, err := tx.Exec(ctx, `
UPDATE hidden_secret_progress
SET revealed = $3,
    completed = $4,
    completed_at = CASE WHEN $4 THEN now() ELSE completed_at END
WHERE user_id = $1 AND secret_id = $2`, userID, secretID, revealed, completed); err != nil {
		log.Printf("[DEBUG] RevealHiddenSecretTile: update revealed failed: %v", err)
		return nil, false, 0, fmt.Errorf("update revealed tiles: %w", err)
	}

	if completed {
		if _, err := tx.Exec(ctx, `
INSERT INTO hidden_secret_unlocks (user_id, secret_id, image_id)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, secret_id) DO NOTHING`, userID, secretID, h.ImageID); err != nil {
			log.Printf("[DEBUG] RevealHiddenSecretTile: unlock picture failed: %v", err)
			return nil, false, 0, fmt.Errorf("unlock hidden secret picture: %w", err)
		}
		xpReward = h.XPReward
		log.Printf("[DEBUG] RevealHiddenSecretTile: unlocked imageID=%v for userID=%v secretID=%v", h.ImageID, userID, secretID)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] RevealHiddenSecretTile: commit failed: %v", err)
		return nil, false, 0, fmt.Errorf("commit reveal: %w", err)
	}
	log.Printf("[DEBUG] RevealHiddenSecretTile: userID=%v secretID=%v tile=%d revealed=%d/%d completed=%t",
		userID, secretID, tileIndex, len(revealed), models.HiddenSecretTileCount, completed)
	return revealed, completed, xpReward, nil
}

// ListUnlockedHiddenSecrets returns every picture the caller has fully
// revealed, newest first.
func (s *Store) ListUnlockedHiddenSecrets(ctx context.Context, userID uuid.UUID) ([]models.HiddenSecretUnlock, error) {
	log.Printf("[DEBUG] ListUnlockedHiddenSecrets: userID=%v", userID)
	rows, err := s.pool.Query(ctx, `
SELECT u.secret_id, h.title, u.image_id, COALESCE(i.url, ''), u.unlocked_at
FROM hidden_secret_unlocks u
JOIN hidden_secrets h ON h.id = u.secret_id
JOIN images i ON i.id = u.image_id
WHERE u.user_id = $1
ORDER BY u.unlocked_at DESC`, userID)
	if err != nil {
		log.Printf("[DEBUG] ListUnlockedHiddenSecrets: query failed: %v", err)
		return nil, fmt.Errorf("list unlocked hidden secrets: %w", err)
	}
	defer rows.Close()

	out := make([]models.HiddenSecretUnlock, 0)
	for rows.Next() {
		var u models.HiddenSecretUnlock
		if err := rows.Scan(&u.SecretID, &u.Title, &u.ImageID, &u.ImageURL, &u.UnlockedAt); err != nil {
			log.Printf("[DEBUG] ListUnlockedHiddenSecrets: scan failed: %v", err)
			return nil, fmt.Errorf("scan unlocked hidden secret: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListUnlockedHiddenSecrets: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] ListUnlockedHiddenSecrets: userID=%v returned %d unlocks", userID, len(out))
	return out, nil
}

// --- Admin -------------------------------------------------------------

// AdminListHiddenSecrets returns every hidden secret (including inactive ones)
// for the admin editor, newest first.
func (s *Store) AdminListHiddenSecrets(ctx context.Context, limit, offset int) ([]models.AdminHiddenSecret, int, error) {
	log.Printf("[DEBUG] AdminListHiddenSecrets: querying limit=%d offset=%d", limit, offset)
	rows, err := s.pool.Query(ctx, `
SELECT h.id, h.title, h.subtitle, h.image_id, COALESCE(i.url, ''), h.xp_reward,
       h.tile_grid_cols, h.tile_grid_rows, h.tile_play_seconds, h.active,
       count(*) OVER ()
FROM hidden_secrets h
JOIN images i ON i.id = h.image_id
ORDER BY h.created_at DESC
LIMIT NULLIF($1, 0) OFFSET $2`, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] AdminListHiddenSecrets: query failed: %v", err)
		return nil, 0, fmt.Errorf("admin list hidden secrets: %w", err)
	}
	defer rows.Close()

	out := make([]models.AdminHiddenSecret, 0)
	total := 0
	for rows.Next() {
		var h models.AdminHiddenSecret
		if err := rows.Scan(&h.ID, &h.Title, &h.Subtitle, &h.ImageID, &h.ImageURL, &h.XPReward,
			&h.TileGridCols, &h.TileGridRows, &h.TilePlaySeconds, &h.Active, &total); err != nil {
			log.Printf("[DEBUG] AdminListHiddenSecrets: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan admin hidden secret: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] AdminListHiddenSecrets: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] AdminListHiddenSecrets: returned %d of %d secrets", len(out), total)
	return out, total, nil
}

// GetAdminHiddenSecret returns one hidden secret for the admin editor.
func (s *Store) GetAdminHiddenSecret(ctx context.Context, id uuid.UUID) (models.AdminHiddenSecret, error) {
	h, err := s.getHiddenSecretRow(ctx, id)
	if err != nil {
		return models.AdminHiddenSecret{}, err
	}
	return models.AdminHiddenSecret{
		ID: h.ID, Title: h.Title, Subtitle: h.Subtitle,
		ImageID: h.ImageID, ImageURL: h.ImageURL, XPReward: h.XPReward,
		TileGridCols: h.TileGridCols, TileGridRows: h.TileGridRows,
		TilePlaySeconds: h.TilePlaySeconds, Active: h.Active,
	}, nil
}

// CreateHiddenSecret inserts a new hidden secret and returns its id.
func (s *Store) CreateHiddenSecret(ctx context.Context, p SaveHiddenSecretParams) (uuid.UUID, error) {
	log.Printf("[DEBUG] CreateHiddenSecret: title=%q imageID=%v", p.Title, p.ImageID)
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `
INSERT INTO hidden_secrets
    (title, subtitle, image_id, xp_reward, tile_grid_cols, tile_grid_rows, tile_play_seconds, active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id`,
		p.Title, p.Subtitle, p.ImageID, p.XPReward,
		p.TileGridCols, p.TileGridRows, p.TilePlaySeconds, p.Active).Scan(&id); err != nil {
		log.Printf("[DEBUG] CreateHiddenSecret: insert failed: %v", err)
		return uuid.Nil, fmt.Errorf("create hidden secret: %w", err)
	}
	log.Printf("[DEBUG] CreateHiddenSecret: created secretID=%v", id)
	return id, nil
}

// UpdateHiddenSecret replaces a hidden secret's configuration.
//
// Changing the picture invalidates every player's revealed tiles — the crops
// they solved belonged to the old image — so saved progress for this secret is
// dropped when (and only when) the image changes.
func (s *Store) UpdateHiddenSecret(ctx context.Context, id uuid.UUID, p SaveHiddenSecretParams) error {
	log.Printf("[DEBUG] UpdateHiddenSecret: secretID=%v title=%q", id, p.Title)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] UpdateHiddenSecret: begin tx failed: %v", err)
		return fmt.Errorf("begin update tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldImageID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT image_id FROM hidden_secrets WHERE id = $1 FOR UPDATE`, id).Scan(&oldImageID)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] UpdateHiddenSecret: secret not found secretID=%v", id)
		return ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] UpdateHiddenSecret: load current failed: %v", err)
		return fmt.Errorf("load hidden secret for update: %w", err)
	}

	if _, err := tx.Exec(ctx, `
UPDATE hidden_secrets
SET title = $2, subtitle = $3, image_id = $4, xp_reward = $5,
    tile_grid_cols = $6, tile_grid_rows = $7, tile_play_seconds = $8,
    active = $9, updated_at = now()
WHERE id = $1`,
		id, p.Title, p.Subtitle, p.ImageID, p.XPReward,
		p.TileGridCols, p.TileGridRows, p.TilePlaySeconds, p.Active); err != nil {
		log.Printf("[DEBUG] UpdateHiddenSecret: update failed: %v", err)
		return fmt.Errorf("update hidden secret: %w", err)
	}

	if oldImageID != p.ImageID {
		if _, err := tx.Exec(ctx, `
DELETE FROM hidden_secret_progress WHERE secret_id = $1`, id); err != nil {
			log.Printf("[DEBUG] UpdateHiddenSecret: reset progress failed: %v", err)
			return fmt.Errorf("reset hidden secret progress: %w", err)
		}
		log.Printf("[DEBUG] UpdateHiddenSecret: image changed, progress reset secretID=%v", id)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] UpdateHiddenSecret: commit failed: %v", err)
		return fmt.Errorf("commit update: %w", err)
	}
	log.Printf("[DEBUG] UpdateHiddenSecret: success secretID=%v", id)
	return nil
}

// DeleteHiddenSecret removes a hidden secret; its progress and unlocks cascade.
func (s *Store) DeleteHiddenSecret(ctx context.Context, id uuid.UUID) error {
	log.Printf("[DEBUG] DeleteHiddenSecret: secretID=%v", id)
	tag, err := s.pool.Exec(ctx, `DELETE FROM hidden_secrets WHERE id = $1`, id)
	if err != nil {
		log.Printf("[DEBUG] DeleteHiddenSecret: delete failed: %v", err)
		return fmt.Errorf("delete hidden secret: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Printf("[DEBUG] DeleteHiddenSecret: secret not found secretID=%v", id)
		return ErrNotFound
	}
	log.Printf("[DEBUG] DeleteHiddenSecret: success secretID=%v", id)
	return nil
}
