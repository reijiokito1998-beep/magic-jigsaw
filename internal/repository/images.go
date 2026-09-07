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

// CreateImageParams describes a newly uploaded Cloudinary image.
type CreateImageParams struct {
	PublicID    string
	URL         string
	Width       int
	Height      int
	Title       string
	UploadedBy  uuid.NullUUID
	IsChallenge bool
	IsLibrary   bool
}

// CreateImage inserts an image row and returns it.
func (s *Store) CreateImage(ctx context.Context, p CreateImageParams) (models.Image, error) {
	const q = `
INSERT INTO images (public_id, url, width, height, title, uploaded_by, is_challenge, is_library)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, public_id, url, width, height, title, uploaded_by, is_challenge, created_at`

	var img models.Image
	log.Printf("[DEBUG] CreateImage: publicID=%s title=%s uploadedBy=%v isChallenge=%v isLibrary=%v",
		p.PublicID, p.Title, p.UploadedBy, p.IsChallenge, p.IsLibrary)
	err := s.pool.QueryRow(ctx, q,
		p.PublicID, p.URL, p.Width, p.Height, p.Title, p.UploadedBy, p.IsChallenge, p.IsLibrary,
	).Scan(&img.ID, &img.PublicID, &img.URL, &img.Width, &img.Height, &img.Title, &img.UploadedBy, &img.IsChallenge, &img.CreatedAt)
	if err != nil {
		log.Printf("[DEBUG] CreateImage: query failed: %v", err)
		return models.Image{}, fmt.Errorf("create image: %w", err)
	}
	log.Printf("[DEBUG] CreateImage: created image id=%v", img.ID)
	return img, nil
}

// ListLibraryImages returns admin library images (newest first), optionally
// filtered by a title substring.
func (s *Store) ListLibraryImages(ctx context.Context, query string, limit, offset int) ([]models.Image, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
SELECT id, public_id, url, width, height, title, uploaded_by, is_challenge, created_at,
       count(*) OVER ()
FROM images
WHERE is_library = true AND ($1 = '' OR title ILIKE '%' || $1 || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3`
	log.Printf("[DEBUG] ListLibraryImages: query=%q limit=%d offset=%d", query, limit, offset)
	rows, err := s.pool.Query(ctx, q, query, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] ListLibraryImages: query failed: %v", err)
		return nil, 0, fmt.Errorf("list library: %w", err)
	}
	defer rows.Close()
	out := make([]models.Image, 0)
	total := 0
	for rows.Next() {
		var img models.Image
		if err := rows.Scan(&img.ID, &img.PublicID, &img.URL, &img.Width, &img.Height,
			&img.Title, &img.UploadedBy, &img.IsChallenge, &img.CreatedAt, &total); err != nil {
			log.Printf("[DEBUG] ListLibraryImages: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan library: %w", err)
		}
		out = append(out, img)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListLibraryImages: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] ListLibraryImages: returned %d of %d images", len(out), total)
	return out, total, nil
}

// SetImageTitle renames an image.
func (s *Store) SetImageTitle(ctx context.Context, id uuid.UUID, title string) error {
	log.Printf("[DEBUG] SetImageTitle: id=%v title=%s", id, title)
	tag, err := s.pool.Exec(ctx, `UPDATE images SET title = $2 WHERE id = $1`, id, title)
	if err != nil {
		log.Printf("[DEBUG] SetImageTitle: query failed: %v", err)
		return fmt.Errorf("set title: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Printf("[DEBUG] SetImageTitle: not found id=%v", id)
		return ErrNotFound
	}
	log.Printf("[DEBUG] SetImageTitle: rows affected=%d", tag.RowsAffected())
	return nil
}

// SetImageLibrary flags/unflags an image as part of the library. Removing the
// flag hides it from the picker while keeping any games that already use it.
func (s *Store) SetImageLibrary(ctx context.Context, id uuid.UUID, inLibrary bool) error {
	log.Printf("[DEBUG] SetImageLibrary: id=%v inLibrary=%v", id, inLibrary)
	tag, err := s.pool.Exec(ctx, `UPDATE images SET is_library = $2 WHERE id = $1`, id, inLibrary)
	if err != nil {
		log.Printf("[DEBUG] SetImageLibrary: query failed: %v", err)
		return fmt.Errorf("set library: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Printf("[DEBUG] SetImageLibrary: not found id=%v", id)
		return ErrNotFound
	}
	log.Printf("[DEBUG] SetImageLibrary: rows affected=%d", tag.RowsAffected())
	return nil
}

// GetImage fetches an image by id.
func (s *Store) GetImage(ctx context.Context, id uuid.UUID) (models.Image, error) {
	const q = `
SELECT id, public_id, url, width, height, title, uploaded_by, is_challenge, created_at
FROM images WHERE id = $1`

	log.Printf("[DEBUG] GetImage: id=%v", id)
	var img models.Image
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&img.ID, &img.PublicID, &img.URL, &img.Width, &img.Height, &img.Title, &img.UploadedBy, &img.IsChallenge, &img.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetImage: not found id=%v", id)
		return models.Image{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] GetImage: query failed: %v", err)
		return models.Image{}, fmt.Errorf("get image: %w", err)
	}
	return img, nil
}
