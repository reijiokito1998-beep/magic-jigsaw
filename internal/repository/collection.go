package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/models"
)

// AddToCollection saves an image into a user's collection (idempotent).
func (s *Store) AddToCollection(ctx context.Context, userID, imageID uuid.UUID) error {
	const q = `
INSERT INTO collection_items (user_id, image_id)
VALUES ($1, $2)
ON CONFLICT (user_id, image_id) DO NOTHING`
	log.Printf("[DEBUG] AddToCollection: userID=%v imageID=%v", userID, imageID)
	tag, err := s.pool.Exec(ctx, q, userID, imageID)
	if err != nil {
		log.Printf("[DEBUG] AddToCollection: query failed: %v", err)
		return fmt.Errorf("add to collection: %w", err)
	}
	log.Printf("[DEBUG] AddToCollection: rows affected=%d", tag.RowsAffected())
	return nil
}

// RemoveFromCollection deletes an image from a user's collection.
func (s *Store) RemoveFromCollection(ctx context.Context, userID, imageID uuid.UUID) error {
	const q = `DELETE FROM collection_items WHERE user_id = $1 AND image_id = $2`
	log.Printf("[DEBUG] RemoveFromCollection: userID=%v imageID=%v", userID, imageID)
	tag, err := s.pool.Exec(ctx, q, userID, imageID)
	if err != nil {
		log.Printf("[DEBUG] RemoveFromCollection: query failed: %v", err)
		return fmt.Errorf("remove from collection: %w", err)
	}
	log.Printf("[DEBUG] RemoveFromCollection: rows affected=%d", tag.RowsAffected())
	return nil
}

// ListCollection returns a user's saved images, newest first.
func (s *Store) ListCollection(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.CollectionItem, int, error) {
	const q = `
SELECT ci.id, ci.added_at,
       i.id, i.public_id, i.url, i.width, i.height, i.title, i.uploaded_by, i.is_challenge, i.created_at,
       count(*) OVER ()
FROM collection_items ci
JOIN images i ON i.id = ci.image_id
WHERE ci.user_id = $1
ORDER BY ci.added_at DESC
LIMIT NULLIF($2, 0) OFFSET $3`

	log.Printf("[DEBUG] ListCollection: userID=%v limit=%d offset=%d", userID, limit, offset)
	rows, err := s.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] ListCollection: query failed: %v", err)
		return nil, 0, fmt.Errorf("list collection: %w", err)
	}
	defer rows.Close()

	items := make([]models.CollectionItem, 0)
	total := 0
	for rows.Next() {
		var it models.CollectionItem
		var img models.Image
		if err := rows.Scan(
			&it.ID, &it.AddedAt,
			&img.ID, &img.PublicID, &img.URL, &img.Width, &img.Height, &img.Title, &img.UploadedBy, &img.IsChallenge, &img.CreatedAt,
			&total,
		); err != nil {
			log.Printf("[DEBUG] ListCollection: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan collection item: %w", err)
		}
		it.Image = img
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListCollection: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] ListCollection: returned %d of %d items for userID=%v", len(items), total, userID)
	return items, total, nil
}
