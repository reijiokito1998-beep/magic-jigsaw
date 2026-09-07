package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
)

// AwardAchievement records an unlocked achievement (idempotent).
func (s *Store) AwardAchievement(ctx context.Context, userID uuid.UUID, key string) error {
	const q = `
INSERT INTO achievements (user_id, key) VALUES ($1, $2)
ON CONFLICT (user_id, key) DO NOTHING`
	log.Printf("[DEBUG] AwardAchievement: awarding userID=%v key=%s", userID, key)
	tag, err := s.pool.Exec(ctx, q, userID, key)
	if err != nil {
		log.Printf("[DEBUG] AwardAchievement: query failed: %v", err)
		return fmt.Errorf("award achievement: %w", err)
	}
	log.Printf("[DEBUG] AwardAchievement: rows affected=%d", tag.RowsAffected())
	return nil
}

// GetAchievements returns the set of unlocked achievement keys for a user.
func (s *Store) GetAchievements(ctx context.Context, userID uuid.UUID) (map[string]bool, error) {
	log.Printf("[DEBUG] GetAchievements: querying userID=%v", userID)
	rows, err := s.pool.Query(ctx, `SELECT key FROM achievements WHERE user_id = $1`, userID)
	if err != nil {
		log.Printf("[DEBUG] GetAchievements: query failed: %v", err)
		return nil, fmt.Errorf("get achievements: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			log.Printf("[DEBUG] GetAchievements: scan failed: %v", err)
			return nil, fmt.Errorf("scan achievement: %w", err)
		}
		out[key] = true
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] GetAchievements: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] GetAchievements: found %d achievements for userID=%v", len(out), userID)
	return out, nil
}
