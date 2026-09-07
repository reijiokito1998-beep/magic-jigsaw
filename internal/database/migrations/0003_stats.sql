-- Progression, scoring and achievements.

-- Experience points (100 per completed challenge). Level is derived from this.
ALTER TABLE users ADD COLUMN IF NOT EXISTS xp INT NOT NULL DEFAULT 0;

-- Challenge grid dimensions (piece count = cols * rows), set by the admin.
ALTER TABLE daily_challenges ADD COLUMN IF NOT EXISTS grid_cols INT NOT NULL DEFAULT 6;
ALTER TABLE daily_challenges ADD COLUMN IF NOT EXISTS grid_rows INT NOT NULL DEFAULT 10;

-- How many pieces the user placed correctly in this challenge (counts toward
-- the global score whether or not the challenge was completed).
ALTER TABLE challenge_results ADD COLUMN IF NOT EXISTS correct_pieces INT NOT NULL DEFAULT 0;

-- Unlocked achievements (first_piece | speed_demon | jigsaw_king).
CREATE TABLE IF NOT EXISTS achievements (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    unlocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
