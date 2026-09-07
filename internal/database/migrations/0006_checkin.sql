-- Daily check-in: earns 1 "unlock point" per day (a currency for later use).
ALTER TABLE users ADD COLUMN IF NOT EXISTS unlock_points INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS checkins (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    check_date DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, check_date)
);

CREATE INDEX IF NOT EXISTS idx_checkins_user ON checkins(user_id);
