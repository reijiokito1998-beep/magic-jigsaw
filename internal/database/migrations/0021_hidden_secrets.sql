-- Hidden Secret mode: an admin-authored picture hidden behind a 3x3 wall of 9
-- shutters. Each shutter is opened by solving a jigsaw puzzle cut from that
-- shutter's own crop of the picture, so tile 4 (centre) assembles exactly the
-- part of the image it was hiding. Revealing all 9 awards XP and unlocks the
-- finished picture into the player's Hidden Secret gallery.
--
-- Tiles are not rows: the grid is a fixed 3x3 (see models.HiddenSecretTileCount)
-- and a tile is addressed by its index 0..8 in row-major order. Only the
-- per-user set of revealed indexes is stored.
--
-- The per-tile puzzle grid (tile_grid_cols/rows) and timer (tile_play_seconds)
-- are set once per secret and shared by all 9 tiles — every shutter is the same
-- size, so a per-tile difficulty would only be arbitrary.

CREATE TABLE IF NOT EXISTS hidden_secrets (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title             TEXT NOT NULL DEFAULT '',
    subtitle          TEXT NOT NULL DEFAULT '',
    image_id          UUID NOT NULL REFERENCES images(id),
    xp_reward         INT  NOT NULL DEFAULT 100,
    tile_grid_cols    INT  NOT NULL DEFAULT 3,
    tile_grid_rows    INT  NOT NULL DEFAULT 3,
    tile_play_seconds INT  NOT NULL DEFAULT 120,
    active            BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_hidden_secrets_active
    ON hidden_secrets(active, created_at DESC);

-- Per-user reveal state. Lazily created on the player's first solved tile (see
-- repository.RevealHiddenSecretTile), so an untouched secret costs no rows.
CREATE TABLE IF NOT EXISTS hidden_secret_progress (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret_id    UUID NOT NULL REFERENCES hidden_secrets(id) ON DELETE CASCADE,
    revealed     JSONB NOT NULL DEFAULT '[]', -- array of tile indexes 0..8
    completed    BOOLEAN NOT NULL DEFAULT false,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, secret_id)
);

-- One unlocked picture per user per fully revealed secret — the gallery of
-- everything the player has uncovered.
CREATE TABLE IF NOT EXISTS hidden_secret_unlocks (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret_id   UUID NOT NULL REFERENCES hidden_secrets(id) ON DELETE CASCADE,
    image_id    UUID NOT NULL REFERENCES images(id),
    unlocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, secret_id)
);

CREATE INDEX IF NOT EXISTS idx_hidden_secret_unlocks_user
    ON hidden_secret_unlocks(user_id, unlocked_at DESC);
