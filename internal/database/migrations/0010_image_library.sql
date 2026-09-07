-- Admin-managed image library: reusable images uploaded once, picked when
-- creating challenges / stories / expedition levels.
ALTER TABLE images ADD COLUMN IF NOT EXISTS is_library BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_images_library ON images(is_library, created_at DESC);
