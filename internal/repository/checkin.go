package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CheckIn records today's check-in (idempotent per day). Returns true if it was
// a new check-in (in which case +1 unlock point was granted).
func (s *Store) CheckIn(ctx context.Context, userID uuid.UUID, date string) (bool, error) {
	log.Printf("[DEBUG] CheckIn: userID=%v date=%s", userID, date)
	tag, err := s.pool.Exec(ctx, `
INSERT INTO checkins (user_id, check_date) VALUES ($1, $2::date)
ON CONFLICT (user_id, check_date) DO NOTHING`, userID, date)
	if err != nil {
		log.Printf("[DEBUG] CheckIn: query failed: %v", err)
		return false, fmt.Errorf("checkin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Printf("[DEBUG] CheckIn: userID=%v already checked in on %s", userID, date)
		return false, nil // already checked in today
	}
	if _, err := s.AddUnlockPoints(ctx, userID, 1); err != nil {
		log.Printf("[DEBUG] CheckIn: grant unlock point failed: %v", err)
		return true, fmt.Errorf("grant unlock point: %w", err)
	}
	log.Printf("[DEBUG] CheckIn: userID=%v new checkin recorded, +1 unlock point", userID)
	return true, nil
}

// AddUnlockPoints credits [amount] unlock points and returns the new balance.
// Callers are responsible for granting only once (e.g. behind a status
// transition), since this has no idempotency of its own.
func (s *Store) AddUnlockPoints(ctx context.Context, userID uuid.UUID, amount int) (int, error) {
	if amount <= 0 {
		return s.GetUnlockPoints(ctx, userID)
	}
	var balance int
	if err := s.pool.QueryRow(ctx, `
UPDATE users SET unlock_points = unlock_points + $2, updated_at = now()
WHERE id = $1
RETURNING unlock_points`, userID, amount).Scan(&balance); err != nil {
		log.Printf("[DEBUG] AddUnlockPoints: query failed: %v", err)
		return 0, fmt.Errorf("add unlock points: %w", err)
	}
	log.Printf("[DEBUG] AddUnlockPoints: userID=%v +%d new balance=%d", userID, amount, balance)
	return balance, nil
}

// GetUnlockPoints returns a user's unlock-point balance.
func (s *Store) GetUnlockPoints(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT unlock_points FROM users WHERE id = $1`, userID).Scan(&n); err != nil {
		log.Printf("[DEBUG] GetUnlockPoints: query failed: %v", err)
		return 0, fmt.Errorf("unlock points: %w", err)
	}
	return n, nil
}

// SpendUnlockPoints deducts [amount] points atomically if the balance is
// sufficient. Returns the new balance and ok=false when there aren't enough.
func (s *Store) SpendUnlockPoints(ctx context.Context, userID uuid.UUID, amount int) (int, bool, error) {
	if amount <= 0 {
		n, err := s.GetUnlockPoints(ctx, userID)
		return n, true, err
	}
	log.Printf("[DEBUG] SpendUnlockPoints: userID=%v amount=%d", userID, amount)
	var balance int
	err := s.pool.QueryRow(ctx, `
UPDATE users SET unlock_points = unlock_points - $2, updated_at = now()
WHERE id = $1 AND unlock_points >= $2
RETURNING unlock_points`, userID, amount).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] SpendUnlockPoints: userID=%v insufficient balance for amount=%d", userID, amount)
		return 0, false, nil // not enough points
	}
	if err != nil {
		log.Printf("[DEBUG] SpendUnlockPoints: query failed: %v", err)
		return 0, false, fmt.Errorf("spend points: %w", err)
	}
	log.Printf("[DEBUG] SpendUnlockPoints: userID=%v new balance=%d", userID, balance)
	return balance, true, nil
}

// GetCheckinDates returns check-in dates (>= since) as "YYYY-MM-DD" strings.
func (s *Store) GetCheckinDates(ctx context.Context, userID uuid.UUID, since string) ([]string, error) {
	log.Printf("[DEBUG] GetCheckinDates: userID=%v since=%s", userID, since)
	rows, err := s.pool.Query(ctx, `
SELECT to_char(check_date, 'YYYY-MM-DD')
FROM checkins WHERE user_id = $1 AND check_date >= $2::date
ORDER BY check_date`, userID, since)
	if err != nil {
		log.Printf("[DEBUG] GetCheckinDates: query failed: %v", err)
		return nil, fmt.Errorf("checkin dates: %w", err)
	}
	defer rows.Close()
	return scanDateStrings(rows)
}

// GetChallengeCompletionDates returns distinct days (>= since) the user
// completed a daily challenge, as "YYYY-MM-DD" strings.
func (s *Store) GetChallengeCompletionDates(ctx context.Context, userID uuid.UUID, since string) ([]string, error) {
	log.Printf("[DEBUG] GetChallengeCompletionDates: userID=%v since=%s", userID, since)
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT to_char(finished_at::date, 'YYYY-MM-DD')
FROM challenge_results
WHERE user_id = $1 AND status = 'completed' AND finished_at IS NOT NULL AND finished_at >= $2::date`,
		userID, since)
	if err != nil {
		log.Printf("[DEBUG] GetChallengeCompletionDates: query failed: %v", err)
		return nil, fmt.Errorf("challenge dates: %w", err)
	}
	defer rows.Close()
	return scanDateStrings(rows)
}

func scanDateStrings(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]string, error) {
	out := make([]string, 0)
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			log.Printf("[DEBUG] scanDateStrings: scan failed: %v", err)
			return nil, fmt.Errorf("scan date: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] scanDateStrings: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] scanDateStrings: returned %d dates", len(out))
	return out, nil
}
