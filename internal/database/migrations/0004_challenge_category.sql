-- Optional category label for challenges (Nature | Space | Architecture | Art).
ALTER TABLE daily_challenges ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
