-- Expedition: a fixed 100-level campaign. Metadata (tier/grid/time) is derived
-- from the level index in code; only the image per level is stored here.
CREATE TABLE IF NOT EXISTS expedition_levels (
    level_index INT PRIMARY KEY CHECK (level_index BETWEEN 1 AND 100),
    image_id    UUID REFERENCES images(id) ON DELETE SET NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the 100 slots (images uploaded by admin later).
INSERT INTO expedition_levels (level_index)
SELECT g FROM generate_series(1, 100) g
ON CONFLICT (level_index) DO NOTHING;

-- Per-user progress on each expedition level.
CREATE TABLE IF NOT EXISTS expedition_progress (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level_index     INT  NOT NULL,
    status          TEXT NOT NULL, -- in_progress | completed | failed
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,
    elapsed_seconds INT NOT NULL DEFAULT 0,
    correct_pieces  INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, level_index)
);

CREATE INDEX IF NOT EXISTS idx_expedition_progress_user ON expedition_progress(user_id);
