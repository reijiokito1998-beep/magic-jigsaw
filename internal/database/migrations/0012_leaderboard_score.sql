-- Denormalize the leaderboard score onto users.total_score.
--
-- The leaderboard and rank queries previously aggregated
--   SUM(correct_pieces) GROUP BY user_id
-- across the entire challenge_results table on every request. That does not
-- scale. We now keep a running total on the user row, updated in the same
-- transaction as every challenge result write (see CompleteResult / FailResult),
-- so reads become a simple indexed ORDER BY / COUNT.

ALTER TABLE users ADD COLUMN IF NOT EXISTS total_score BIGINT NOT NULL DEFAULT 0;

-- Backfill / self-heal from existing results. This matches the previous
-- leaderboard/rank formula exactly: sum of correct_pieces across ALL results (no
-- status filter, because both completed and failed attempts recorded
-- correct_pieces).
--
-- The migration runner re-executes every file on each boot, so this is written
-- to only touch rows whose stored total actually differs from the computed sum:
-- it fully backfills on first run, writes nothing once in sync, and self-heals
-- any drift. Users with no results keep the column default (0).
UPDATE users u
SET total_score = s.score
FROM (
    SELECT user_id, SUM(correct_pieces) AS score
    FROM challenge_results
    GROUP BY user_id
) s
WHERE u.id = s.user_id AND u.total_score <> s.score;

-- Leaderboard ordering and rank comparison both use (total_score, created_at).
CREATE INDEX IF NOT EXISTS idx_users_score ON users (total_score DESC, created_at ASC);

-- The aggregation-support index is no longer needed now that the leaderboard
-- and rank queries read the denormalized column instead of scanning results.
DROP INDEX IF EXISTS idx_results_user_score;
