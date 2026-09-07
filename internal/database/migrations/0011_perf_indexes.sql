-- Performance indexes for the hot read paths.
--
-- The pattern below is "filter by user_id, then sort" — a single-column index
-- on user_id only satisfies the filter and leaves Postgres to sort the matched
-- rows in memory. A composite (user_id, <sort_col>) index serves both the
-- filter and the ordering from one index scan.
--
-- This migration also drops single-column indexes that are now redundant,
-- either because a composite here supersets them or because an existing PRIMARY
-- KEY / UNIQUE constraint already indexes the same leading column. Fewer
-- indexes means cheaper INSERT/UPDATE and less bloat.
--
-- NOTE: these run non-concurrently inside the migration transaction, which is
-- fine on small/medium tables. On a large production table, build the new
-- indexes with CREATE INDEX CONCURRENTLY (outside a transaction) instead to
-- avoid write locks.

-- challenge_results: GET /me/history and GET /me/results filter by user_id and
-- sort by started_at DESC. Composite serves filter + sort in one scan.
CREATE INDEX IF NOT EXISTS idx_results_user_started
    ON challenge_results (user_id, started_at DESC);

-- NOTE: the leaderboard/rank score is denormalized onto users.total_score in
-- migration 0012, so there is no longer a whole-table SUM(correct_pieces)
-- aggregate to support with an index here.

-- collection_items: GET /collection filters by user_id and sorts by added_at DESC.
CREATE INDEX IF NOT EXISTS idx_collection_user_added
    ON collection_items (user_id, added_at DESC);

-- Redundant indexes -----------------------------------------------------------

-- Superseded by idx_results_user_started (user_id is its leading column).
DROP INDEX IF EXISTS idx_results_user;

-- Superseded by idx_collection_user_added (user_id is its leading column).
DROP INDEX IF EXISTS idx_collection_user;

-- Redundant with PRIMARY KEY (user_id, level_index) — user_id is the prefix.
DROP INDEX IF EXISTS idx_expedition_progress_user;

-- Redundant with PRIMARY KEY (user_id, check_date) — user_id is the prefix.
DROP INDEX IF EXISTS idx_checkins_user;

-- Redundant with UNIQUE (story_id, page_index) — story_id is the prefix.
DROP INDEX IF EXISTS idx_story_pages_story;
