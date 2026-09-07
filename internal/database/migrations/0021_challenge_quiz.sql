-- Daily challenge reward change + bonus quiz.
--
-- Completing a daily challenge used to count as an expedition star (see the
-- daily term that TotalExpeditionStars added). It now grants an unlock point
-- instead, and the manager can attach a two-option question to the challenge:
-- answering it correctly grants one more unlock point.

-- 1. The quiz, configured per challenge by the manager. An empty question means
--    "no quiz" — every existing challenge, and any new one left blank.
--    quiz_correct_option is 0 (option A) or 1 (option B); it is never sent to
--    a player, only to the admin screens.
ALTER TABLE daily_challenges
    ADD COLUMN IF NOT EXISTS quiz_question TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quiz_option_a TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quiz_option_b TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quiz_correct_option SMALLINT NOT NULL DEFAULT 0;

-- 2. The player's single answer, recorded on their challenge result. NULL means
--    unanswered, which is what makes the bonus point grantable exactly once.
ALTER TABLE challenge_results
    ADD COLUMN IF NOT EXISTS quiz_answer SMALLINT,
    ADD COLUMN IF NOT EXISTS quiz_answered_at TIMESTAMPTZ;

-- 3. Freeze the stars players already earned from daily challenges.
--    TotalExpeditionStars no longer counts completed challenges, so without
--    this every existing player would silently lose stars and see expedition
--    levels they had already unlocked re-lock. Folding the historical count
--    into expedition_bonus_stars (until now: purchased stars only) keeps every
--    balance exactly where it is; only future challenges pay out differently.
--
--    Migrations re-run on every boot, so the backfill is guarded by a flag
--    rather than by the file having run before: without it each restart would
--    hand every player their daily stars all over again.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS daily_stars_folded_in BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE users u
SET expedition_bonus_stars = u.expedition_bonus_stars + c.n,
    updated_at = now()
FROM (
    SELECT user_id, COUNT(*) AS n
    FROM challenge_results
    WHERE status = 'completed'
    GROUP BY user_id
) c
WHERE c.user_id = u.id AND NOT u.daily_stars_folded_in;

UPDATE users SET daily_stars_folded_in = TRUE WHERE NOT daily_stars_folded_in;
