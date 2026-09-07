-- Per-challenge opt-in for the disruption events ("Bật thử thách"): ink splash,
-- fog, earthquake and quick-countdown interrupt the player while solving.
--
-- Set by the admin who creates/edits the challenge, so everyone attempting a
-- given day plays under the same rules. Existing challenges default to off,
-- which is exactly how they were already played.
ALTER TABLE daily_challenges
    ADD COLUMN IF NOT EXISTS challenge_events_enabled BOOLEAN NOT NULL DEFAULT FALSE;
