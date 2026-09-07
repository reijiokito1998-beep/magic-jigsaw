-- Per-page time limit for story chapters. Existing pages default to 5 minutes.
ALTER TABLE story_pages ADD COLUMN IF NOT EXISTS play_seconds INT NOT NULL DEFAULT 300;
