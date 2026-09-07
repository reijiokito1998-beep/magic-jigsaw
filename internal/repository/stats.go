package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/reijiokito/jigsaw-backend/internal/models"
)

// AggregateStats holds raw aggregates over a user's challenge results.
type AggregateStats struct {
	Score          int // total correct pieces across all results
	PuzzlesSolved  int // completed count
	SolvedThisWeek int // completed in the last 7 days
	Played         int // total results
	TotalTime      int // sum of elapsed seconds
	HighestScore   int // total correct pieces across all results (cumulative)
	SpeedDemon     bool
}

// GetAggregateStats computes a user's play aggregates in one query.
func (s *Store) GetAggregateStats(ctx context.Context, userID uuid.UUID) (AggregateStats, error) {
	const q = `
SELECT
  COALESCE(SUM(cr.correct_pieces), 0)                                        AS score,
  COUNT(*) FILTER (WHERE cr.status = 'completed')                            AS solved,
  COUNT(*) FILTER (WHERE cr.status = 'completed'
                   AND cr.finished_at >= now() - interval '7 days')          AS solved_week,
  COUNT(*)                                                                   AS played,
  COALESCE(SUM(cr.elapsed_seconds), 0)                                       AS total_time,
  COALESCE(SUM(cr.correct_pieces), 0)                                        AS highest,
  COALESCE(BOOL_OR(cr.status = 'completed' AND cr.elapsed_seconds * 2 <= dc.play_seconds), false) AS speed_demon
FROM challenge_results cr
JOIN daily_challenges dc ON dc.id = cr.challenge_id
WHERE cr.user_id = $1`

	log.Printf("[DEBUG] GetAggregateStats: userID=%v", userID)
	var a AggregateStats
	err := s.pool.QueryRow(ctx, q, userID).Scan(
		&a.Score, &a.PuzzlesSolved, &a.SolvedThisWeek, &a.Played,
		&a.TotalTime, &a.HighestScore, &a.SpeedDemon,
	)
	if err != nil {
		log.Printf("[DEBUG] GetAggregateStats: query failed: %v", err)
		return AggregateStats{}, fmt.Errorf("aggregate stats: %w", err)
	}
	log.Printf("[DEBUG] GetAggregateStats: userID=%v score=%d solved=%d played=%d",
		userID, a.Score, a.PuzzlesSolved, a.Played)
	return a, nil
}

// GetRank returns the user's 1-based rank by total score (sum of correct
// pieces across all challenges). Returns 0 when unranked.
func (s *Store) GetRank(ctx context.Context, userID uuid.UUID) (int, error) {
	// Reads the denormalized users.total_score (kept in sync on every result
	// write) instead of aggregating challenge_results. The COUNT uses
	// idx_users_score as a range scan.
	const q = `
SELECT CASE
  WHEN (SELECT total_score FROM users WHERE id = $1) = 0 THEN 0
  ELSE 1 + (SELECT COUNT(*) FROM users WHERE total_score > (SELECT total_score FROM users WHERE id = $1))
END`
	var rank int
	if err := s.pool.QueryRow(ctx, q, userID).Scan(&rank); err != nil {
		log.Printf("[DEBUG] GetRank: query failed: %v", err)
		return 0, fmt.Errorf("get rank: %w", err)
	}
	log.Printf("[DEBUG] GetRank: userID=%v rank=%d", userID, rank)
	return rank, nil
}

// GetLeaderboard returns the top entries ordered by total score desc (sum of
// correct pieces across all challenges).
func (s *Store) GetLeaderboard(ctx context.Context, limit int, meID uuid.UUID) ([]models.LeaderboardEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// Reads the denormalized users.total_score directly; ordering is served by
	// idx_users_score (total_score DESC, created_at ASC) — no table scan/aggregate.
	const q = `
SELECT id, name, xp, total_score
FROM users
ORDER BY total_score DESC, created_at ASC
LIMIT $1`

	log.Printf("[DEBUG] GetLeaderboard: limit=%d meID=%v", limit, meID)
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		log.Printf("[DEBUG] GetLeaderboard: query failed: %v", err)
		return nil, fmt.Errorf("leaderboard: %w", err)
	}
	defer rows.Close()

	out := make([]models.LeaderboardEntry, 0)
	rank := 0
	for rows.Next() {
		rank++
		var e models.LeaderboardEntry
		var xp int
		if err := rows.Scan(&e.UserID, &e.Name, &xp, &e.Score); err != nil {
			log.Printf("[DEBUG] GetLeaderboard: scan failed: %v", err)
			return nil, fmt.Errorf("scan leaderboard: %w", err)
		}
		e.Level, _, _ = models.LevelFromXP(xp)
		e.Rank = rank
		e.IsMe = e.UserID == meID
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] GetLeaderboard: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] GetLeaderboard: returned %d entries", len(out))
	return out, nil
}

// ListHistory returns a user's recently played challenges, newest first.
func (s *Store) ListHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.HistoryItem, int, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
SELECT dc.id, dc.title, i.url, i.id, dc.grid_cols * dc.grid_rows AS pieces,
       dc.grid_cols, dc.grid_rows,
       cr.status, cr.elapsed_seconds, cr.correct_pieces, cr.finished_at,
       count(*) OVER ()
FROM challenge_results cr
JOIN daily_challenges dc ON dc.id = cr.challenge_id
JOIN images i ON i.id = dc.image_id
WHERE cr.user_id = $1
ORDER BY cr.started_at DESC
LIMIT $2 OFFSET $3`

	log.Printf("[DEBUG] ListHistory: userID=%v limit=%d offset=%d", userID, limit, offset)
	rows, err := s.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] ListHistory: query failed: %v", err)
		return nil, 0, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()

	out := make([]models.HistoryItem, 0)
	total := 0
	for rows.Next() {
		var h models.HistoryItem
		if err := rows.Scan(&h.ChallengeID, &h.Title, &h.ImageURL, &h.ImageID, &h.Pieces,
			&h.GridCols, &h.GridRows,
			&h.Status, &h.ElapsedSeconds, &h.CorrectPieces, &h.FinishedAt, &total); err != nil {
			log.Printf("[DEBUG] ListHistory: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan history: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListHistory: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] ListHistory: returned %d of %d items for userID=%v", len(out), total, userID)
	return out, total, nil
}
