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

// The column list and the FROM clause are kept apart so a paginated query can
// slot `count(*) OVER ()` into the SELECT without restating either.
const challengeColumns = `dc.id, dc.challenge_date, dc.play_seconds, dc.grid_cols, dc.grid_rows, dc.category, dc.title, dc.xp_reward,
       dc.challenge_events_enabled, dc.quiz_question, dc.quiz_option_a, dc.quiz_option_b, dc.quiz_correct_option,
       dc.created_at,
       i.id, i.public_id, i.url, i.width, i.height, i.title, i.uploaded_by, i.is_challenge, i.created_at`

const challengeFrom = `
FROM daily_challenges dc
JOIN images i ON i.id = dc.image_id`

const challengeSelect = `
SELECT ` + challengeColumns + challengeFrom

// challengeSelectWithTotal carries the full row count alongside each row, so a
// page and its total come back in one round trip.
const challengeSelectWithTotal = `
SELECT ` + challengeColumns + `,
       count(*) OVER ()` + challengeFrom

func scanChallenge(row pgx.Row) (models.DailyChallenge, error) {
	var c models.DailyChallenge
	var img models.Image
	err := row.Scan(
		&c.ID, &c.ChallengeDate, &c.PlaySeconds, &c.GridCols, &c.GridRows, &c.Category, &c.Title, &c.XPReward,
		&c.ChallengeEventsEnabled, &c.QuizQuestion, &c.QuizOptionA, &c.QuizOptionB, &c.QuizCorrectOption,
		&c.CreatedAt,
		&img.ID, &img.PublicID, &img.URL, &img.Width, &img.Height, &img.Title, &img.UploadedBy, &img.IsChallenge, &img.CreatedAt,
	)
	if err != nil {
		log.Printf("[DEBUG] scanChallenge: scan failed: %v", err)
		return models.DailyChallenge{}, err
	}
	c.Image = img
	return c, nil
}

// scanChallengeWithTotal scans a row of [challengeSelectWithTotal].
func scanChallengeWithTotal(row pgx.Row) (models.DailyChallenge, int, error) {
	var c models.DailyChallenge
	var img models.Image
	total := 0
	err := row.Scan(
		&c.ID, &c.ChallengeDate, &c.PlaySeconds, &c.GridCols, &c.GridRows, &c.Category, &c.Title, &c.XPReward,
		&c.ChallengeEventsEnabled, &c.QuizQuestion, &c.QuizOptionA, &c.QuizOptionB, &c.QuizCorrectOption,
		&c.CreatedAt,
		&img.ID, &img.PublicID, &img.URL, &img.Width, &img.Height, &img.Title, &img.UploadedBy, &img.IsChallenge, &img.CreatedAt,
		&total,
	)
	if err != nil {
		log.Printf("[DEBUG] scanChallengeWithTotal: scan failed: %v", err)
		return models.DailyChallenge{}, 0, err
	}
	c.Image = img
	return c, total, nil
}

// CreateChallengeParams describes a new daily challenge. Date is a civil date
// in "2006-01-02" form.
type CreateChallengeParams struct {
	Date                   string
	ImageID                uuid.UUID
	PlaySeconds            int
	GridCols               int
	GridRows               int
	Category               string
	Title                  string
	XPReward               int
	ChallengeEventsEnabled bool
	Quiz                   QuizParams
	CreatedBy              uuid.NullUUID
}

// QuizParams is the optional bonus question attached to a challenge. A blank
// Question stores "no quiz"; the API layer validates the options before this
// reaches the database.
type QuizParams struct {
	Question      string
	OptionA       string
	OptionB       string
	CorrectOption int // 0 = A, 1 = B
}

// CreateChallenge inserts a daily challenge (one per date). Returns the row
// including its joined image.
func (s *Store) CreateChallenge(ctx context.Context, p CreateChallengeParams) (models.DailyChallenge, error) {
	const ins = `
INSERT INTO daily_challenges (challenge_date, image_id, play_seconds, grid_cols, grid_rows, category, title, xp_reward, challenge_events_enabled,
                              quiz_question, quiz_option_a, quiz_option_b, quiz_correct_option, created_by)
VALUES ($1::date, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (challenge_date) DO UPDATE
SET image_id = EXCLUDED.image_id,
    play_seconds = EXCLUDED.play_seconds,
    grid_cols = EXCLUDED.grid_cols,
    grid_rows = EXCLUDED.grid_rows,
    category = EXCLUDED.category,
    title = EXCLUDED.title,
    xp_reward = EXCLUDED.xp_reward,
    challenge_events_enabled = EXCLUDED.challenge_events_enabled,
    quiz_question = EXCLUDED.quiz_question,
    quiz_option_a = EXCLUDED.quiz_option_a,
    quiz_option_b = EXCLUDED.quiz_option_b,
    quiz_correct_option = EXCLUDED.quiz_correct_option
RETURNING id`

	var id uuid.UUID
	log.Printf("[DEBUG] CreateChallenge: upserting date=%s imageID=%v events=%t quiz=%t", p.Date, p.ImageID, p.ChallengeEventsEnabled, p.Quiz.Question != "")
	if err := s.pool.QueryRow(ctx, ins, p.Date, p.ImageID, p.PlaySeconds, p.GridCols, p.GridRows, p.Category, p.Title, p.XPReward, p.ChallengeEventsEnabled,
		p.Quiz.Question, p.Quiz.OptionA, p.Quiz.OptionB, p.Quiz.CorrectOption, p.CreatedBy).Scan(&id); err != nil {
		log.Printf("[DEBUG] CreateChallenge: query failed: %v", err)
		return models.DailyChallenge{}, fmt.Errorf("create challenge: %w", err)
	}
	log.Printf("[DEBUG] CreateChallenge: created/updated challenge id=%v", id)
	return s.GetChallenge(ctx, id)
}

// UpdateChallengeParams updates an existing challenge. ImageID is optional
// (keep the current image when not valid).
type UpdateChallengeParams struct {
	ID                     uuid.UUID
	Title                  string
	Category               string
	PlaySeconds            int
	GridCols               int
	GridRows               int
	XPReward               int
	ChallengeEventsEnabled bool
	Quiz                   QuizParams
	ImageID                uuid.NullUUID
}

// UpdateChallenge edits a challenge in place (date unchanged).
func (s *Store) UpdateChallenge(ctx context.Context, p UpdateChallengeParams) (models.DailyChallenge, error) {
	const q = `
UPDATE daily_challenges
SET title = $2, category = $3, play_seconds = $4, grid_cols = $5, grid_rows = $6,
    xp_reward = $7, challenge_events_enabled = $8, image_id = COALESCE($9, image_id),
    quiz_question = $10, quiz_option_a = $11, quiz_option_b = $12, quiz_correct_option = $13
WHERE id = $1
RETURNING id`
	var id uuid.UUID
	log.Printf("[DEBUG] UpdateChallenge: updating id=%v events=%t quiz=%t", p.ID, p.ChallengeEventsEnabled, p.Quiz.Question != "")
	err := s.pool.QueryRow(ctx, q, p.ID, p.Title, p.Category, p.PlaySeconds, p.GridCols, p.GridRows, p.XPReward, p.ChallengeEventsEnabled, p.ImageID,
		p.Quiz.Question, p.Quiz.OptionA, p.Quiz.OptionB, p.Quiz.CorrectOption).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] UpdateChallenge: not found id=%v", p.ID)
		return models.DailyChallenge{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] UpdateChallenge: query failed: %v", err)
		return models.DailyChallenge{}, fmt.Errorf("update challenge: %w", err)
	}
	log.Printf("[DEBUG] UpdateChallenge: updated id=%v", id)
	return s.GetChallenge(ctx, id)
}

// NextChallengeDate returns the next free queue date ("2006-01-02"): the day
// after the last queued challenge, or today if the queue is empty/past.
func (s *Store) NextChallengeDate(ctx context.Context) (string, error) {
	const q = `
SELECT to_char(
  GREATEST(current_date, COALESCE(MAX(challenge_date) + 1, current_date)),
  'YYYY-MM-DD')
FROM daily_challenges`
	var d string
	if err := s.pool.QueryRow(ctx, q).Scan(&d); err != nil {
		log.Printf("[DEBUG] NextChallengeDate: query failed: %v", err)
		return "", fmt.Errorf("next challenge date: %w", err)
	}
	log.Printf("[DEBUG] NextChallengeDate: next date=%s", d)
	return d, nil
}

// GetChallenge fetches a challenge by id.
func (s *Store) GetChallenge(ctx context.Context, id uuid.UUID) (models.DailyChallenge, error) {
	log.Printf("[DEBUG] GetChallenge: querying id=%v", id)
	c, err := scanChallenge(s.pool.QueryRow(ctx, challengeSelect+` WHERE dc.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetChallenge: not found id=%v", id)
		return models.DailyChallenge{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] GetChallenge: query failed: %v", err)
		return models.DailyChallenge{}, fmt.Errorf("get challenge: %w", err)
	}
	return c, nil
}

// GetChallengeByDate fetches the challenge for a specific calendar day, given
// as a "2006-01-02" civil date string.
func (s *Store) GetChallengeByDate(ctx context.Context, date string) (models.DailyChallenge, error) {
	log.Printf("[DEBUG] GetChallengeByDate: querying date=%s", date)
	c, err := scanChallenge(s.pool.QueryRow(ctx, challengeSelect+` WHERE dc.challenge_date = $1::date`, date))
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[DEBUG] GetChallengeByDate: not found date=%s", date)
		return models.DailyChallenge{}, ErrNotFound
	}
	if err != nil {
		log.Printf("[DEBUG] GetChallengeByDate: query failed: %v", err)
		return models.DailyChallenge{}, fmt.Errorf("get challenge by date: %w", err)
	}
	return c, nil
}

// ListChallenges returns recent challenges, newest first.
// ListChallenges returns the challenge queue, newest first. [limit] caps the
// window (0 falls back to the historical 30-row ceiling) and [offset] pages
// through it; total is the row count across all pages.
func (s *Store) ListChallenges(ctx context.Context, limit, offset int) ([]models.DailyChallenge, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	log.Printf("[DEBUG] ListChallenges: querying limit=%d offset=%d", limit, offset)
	rows, err := s.pool.Query(ctx,
		challengeSelectWithTotal+` ORDER BY dc.challenge_date DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		log.Printf("[DEBUG] ListChallenges: query failed: %v", err)
		return nil, 0, fmt.Errorf("list challenges: %w", err)
	}
	defer rows.Close()

	out := make([]models.DailyChallenge, 0)
	total := 0
	for rows.Next() {
		c, rowTotal, err := scanChallengeWithTotal(rows)
		if err != nil {
			log.Printf("[DEBUG] ListChallenges: scan failed: %v", err)
			return nil, 0, fmt.Errorf("scan challenge: %w", err)
		}
		total = rowTotal
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[DEBUG] ListChallenges: rows error: %v", err)
		return nil, 0, err
	}
	log.Printf("[DEBUG] ListChallenges: returned %d of %d challenges", len(out), total)
	return out, total, nil
}
