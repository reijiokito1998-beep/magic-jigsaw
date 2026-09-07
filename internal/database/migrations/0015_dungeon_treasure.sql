-- Dungeon treasure challenge: an optional puzzle at the treasure cell. When a
-- dungeon has a treasure_image_id set, reaching the treasure (all monsters
-- defeated) opens its own puzzle; winning it completes the dungeon and unlocks
-- the treasure image into the per-user "Dungeon Treasure" gallery. Nullable so
-- existing dungeons keep auto-completing at the treasure with no puzzle.

ALTER TABLE dungeons
    ADD COLUMN treasure_image_id     UUID REFERENCES images(id),
    ADD COLUMN treasure_grid_cols    INT NOT NULL DEFAULT 3,
    ADD COLUMN treasure_grid_rows    INT NOT NULL DEFAULT 3,
    ADD COLUMN treasure_play_seconds INT NOT NULL DEFAULT 120;

-- One unlocked treasure image per user per completed dungeon.
CREATE TABLE IF NOT EXISTS dungeon_treasure_unlocks (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dungeon_id  UUID NOT NULL REFERENCES dungeons(id) ON DELETE CASCADE,
    image_id    UUID NOT NULL REFERENCES images(id),
    unlocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, dungeon_id)
);
