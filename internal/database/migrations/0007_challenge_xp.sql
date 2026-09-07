-- XP awarded to a user when they complete this daily challenge.
ALTER TABLE daily_challenges ADD COLUMN IF NOT EXISTS xp_reward INT NOT NULL DEFAULT 100;
