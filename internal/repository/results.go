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

const resultSelect = `
SELECT id, challenge_id, status, started_at, finished_at, elapsed_seconds, correct_pieces, quiz_answer
FROM challenge_results`

func scanResult(row pgx.Row) (models.ChallengeResult, error) {
	var r models.ChallengeResult
	err := row.Scan(&r.ID, &r.ChallengeID, &r.Status, &r.StartedAt, &r.FinishedAt, &r.ElapsedSeconds, &r.CorrectPieces, &r.QuizAnswer)
	if err != nil {
		log.Printf("[DEBUG] scanResult: scan failed: %v", err)
	}
	return r, err
}

// GetResult returns the user's result for a challenge, or ErrNotFound.
func (s *Store) GetResult(ctx context.Context, userID, challengeID uuid.UUID) (models.ChallengeResult, error) {
	log.Printf("[DEBUG] GetResult: userID=%v challengeID=%v", userID, challengeID)
	r, err := scanResult(s.pool.QueryRow(ctx,
		resultSelect+` WHERE user_id = $1 AND challenge_id = $2`, userID, challengeID))
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetResult: not found userID=%v challengeID=%v", userID, challengeID)
		return models.ChallengeResult{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] GetResult: query failed: %v", err)
		return models.ChallengeResult{}, fmt.Errorf("get result: %w", err)
	}
	return r, nil
}

// StartResult marks a challenge as started for the user. If a result already
// exists (in progress, completed, or failed) it is returned unchanged so a
// finished reputation can never be reset.
func (s *Store) StartResult(ctx context.Context, userID, challengeID uuid.UUID) (models.ChallengeResult, error) {
	const q = `
INSERT INTO challenge_results (user_id, challenge_id, status)
VALUES ($1, $2, 'in_progress')
ON CONFLICT (user_id, challenge_id) DO NOTHING`
	log.Printf("[DEBUG] StartResult: userID=%v challengeID=%v", userID, challengeID)
	if _, err := s.pool.Exec(ctx, q, userID, challengeID); err != nil {
		log.Printf("[DEBUG] StartResult: query failed: %v", err)
		return models.ChallengeResult{}, fmt.Errorf("start result: %w", err)
	}
	return s.GetResult(ctx, userID, challengeID)
}

// CompleteResult marks an in-progress challenge as completed. It is a no-op if
// the result is already failed or completed (reputation is immutable once set).
// Returns whether the row actually transitioned (so XP is awarded once).
//
// The user's denormalized leaderboard score (users.total_score) is incremented
// in the SAME transaction, so the two can never drift. The status='in_progress'
// guard ensures a result transitions exactly once, so the score is added once.
func (s *Store) CompleteResult(ctx context.Context, userID, challengeID uuid.UUID, elapsedSeconds, correctPieces int) (models.ChallengeResult, bool, error) {
	const q = `
UPDATE challenge_results
SET status = 'completed', finished_at = now(), correct_pieces = $3, elapsed_seconds = $4
WHERE user_id = $1 AND challenge_id = $2 AND status = 'in_progress'`
	log.Printf("[DEBUG] CompleteResult: userID=%v challengeID=%v elapsedSeconds=%d correctPieces=%d",
		userID, challengeID, elapsedSeconds, correctPieces)
	return s.finishResult(ctx, userID, challengeID, correctPieces,
		q, userID, challengeID, correctPieces, elapsedSeconds)
}

// FailResult marks an in-progress challenge as failed (user quit or lost). It is
// a no-op if the result is already completed or failed. Like CompleteResult, it
// updates the denormalized leaderboard score atomically.
func (s *Store) FailResult(ctx context.Context, userID, challengeID uuid.UUID, correctPieces int) (models.ChallengeResult, error) {
	const q = `
UPDATE challenge_results
SET status = 'failed', finished_at = now(), correct_pieces = $3
WHERE user_id = $1 AND challenge_id = $2 AND status = 'in_progress'`
	log.Printf("[DEBUG] FailResult: userID=%v challengeID=%v correctPieces=%d", userID, challengeID, correctPieces)
	res, _, err := s.finishResult(ctx, userID, challengeID, correctPieces,
		q, userID, challengeID, correctPieces)
	return res, err
}

// finishResult applies a terminal transition (complete or fail) to a result and,
// when the row actually transitioned, adds correctPieces to the user's running
// leaderboard total — all in a single transaction. updateSQL/updateArgs are the
// status-guarded UPDATE on challenge_results; correctPieces is the amount added
// to users.total_score when the row transitions.
func (s *Store) finishResult(ctx context.Context, userID, challengeID uuid.UUID, correctPieces int, updateSQL string, updateArgs ...any) (models.ChallengeResult, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] finishResult: begin tx failed: %v", err)
		return models.ChallengeResult{}, false, fmt.Errorf("finish result: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, updateSQL, updateArgs...)
	if err != nil {
		log.Printf("[DEBUG] finishResult: update query failed: %v", err)
		return models.ChallengeResult{}, false, fmt.Errorf("finish result: %w", err)
	}
	transitioned := tag.RowsAffected() > 0
	log.Printf("[DEBUG] finishResult: userID=%v challengeID=%v transitioned=%v", userID, challengeID, transitioned)

	if transitioned && correctPieces != 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET total_score = total_score + $2, updated_at = now() WHERE id = $1`,
			userID, correctPieces); err != nil {
			log.Printf("[DEBUG] finishResult: score update failed: %v", err)
			return models.ChallengeResult{}, false, fmt.Errorf("finish result: score: %w", err)
		}
		log.Printf("[DEBUG] finishResult: userID=%v score +%d", userID, correctPieces)
	}

	res, err := scanResult(tx.QueryRow(ctx,
		resultSelect+` WHERE user_id = $1 AND challenge_id = $2`, userID, challengeID))
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] finishResult: not found userID=%v challengeID=%v", userID, challengeID)
		return models.ChallengeResult{}, false, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] finishResult: read failed: %v", err)
		return models.ChallengeResult{}, false, fmt.Errorf("finish result: read: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] finishResult: commit failed: %v", err)
		return models.ChallengeResult{}, false, fmt.Errorf("finish result: commit: %w", err)
	}
	return res, transitioned, nil
}

// AnswerChallengeQuiz records the user's answer to a challenge's bonus
// question and, when it is right, grants one unlock point — both in a single
// transaction.
//
// The quiz_answer IS NULL guard makes the whole thing single-shot: a second
// call (double tap, replayed request) finds no row to update and returns
// granted=false, so the point can never be farmed by re-answering.
// Returns the answer already on file when there was one.
func (s *Store) AnswerChallengeQuiz(ctx context.Context, userID, challengeID uuid.UUID, answer int, correct bool) (granted bool, existing *int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[DEBUG] AnswerChallengeQuiz: begin tx failed: %v", err)
		return false, nil, fmt.Errorf("answer quiz: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
UPDATE challenge_results
SET quiz_answer = $3, quiz_answered_at = now()
WHERE user_id = $1 AND challenge_id = $2 AND status = 'completed' AND quiz_answer IS NULL`,
		userID, challengeID, answer)
	if err != nil {
		log.Printf("[DEBUG] AnswerChallengeQuiz: update failed: %v", err)
		return false, nil, fmt.Errorf("answer quiz: %w", err)
	}
	recorded := tag.RowsAffected() > 0

	if recorded && correct {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET unlock_points = unlock_points + 1, updated_at = now() WHERE id = $1`,
			userID); err != nil {
			log.Printf("[DEBUG] AnswerChallengeQuiz: grant unlock point failed: %v", err)
			return false, nil, fmt.Errorf("answer quiz: grant point: %w", err)
		}
	}

	var stored *int
	if err := tx.QueryRow(ctx,
		`SELECT quiz_answer FROM challenge_results WHERE user_id = $1 AND challenge_id = $2`,
		userID, challengeID).Scan(&stored); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[DEBUG] AnswerChallengeQuiz: no result userID=%v challengeID=%v", userID, challengeID)
			return false, nil, ErrNotFound
		}
		log.Printf("[DEBUG] AnswerChallengeQuiz: read failed: %v", err)
		return false, nil, fmt.Errorf("answer quiz: read: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[DEBUG] AnswerChallengeQuiz: commit failed: %v", err)
		return false, nil, fmt.Errorf("answer quiz: commit: %w", err)
	}
	log.Printf("[DEBUG] AnswerChallengeQuiz: userID=%v challengeID=%v answer=%d correct=%t recorded=%t",
		userID, challengeID, answer, correct, recorded)
	return recorded && correct, stored, nil
}

// ListResults returns all reputation entries for a user, newest first.
func (s *Store) ListResults(ctx context.Context, userID uuid.UUID) ([]models.ChallengeResult, error) {
	log.Printf("[DEBUG] ListResults: userID=%v", userID)
	rows, err := s.pool.Query(ctx,
		resultSelect+` WHERE user_id = $1 ORDER BY started_at DESC`, userID)
	if err != nil {
		log.Printf("[DEBUG] ListResults: query failed: %v", err)
		return nil, fmt.Errorf("list results: %w", err)
	}
	defer rows.Close()

	out := make([]models.ChallengeResult, 0)
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListResults: rows error: %v", err)
		return nil, err
	}
	log.Printf("[DEBUG] ListResults: returned %d results for userID=%v", len(out), userID)
	return out, nil
}
