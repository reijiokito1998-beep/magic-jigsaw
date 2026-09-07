-- Dungeon mode: an admin-authored 6x6 grid map with a start cell, a treasure
-- cell, one connected road (ordered path from start to treasure) and a
-- variable number of monsters placed on road cells (bounded by
-- models.DungeonMinMonsterCount/DungeonMaxMonsterCount). Players move one
-- adjacent road cell at a time, spending 1 unlock point per move (see
-- users.unlock_points).
--
-- Column names use "cell_row"/"cell_col" (not "row"/"col") to sidestep
-- Postgres reserved-word quoting; the JSON contract still exposes them as
-- "row"/"col".

CREATE TABLE IF NOT EXISTS dungeons (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title          TEXT NOT NULL DEFAULT '',
    xp_reward      INT  NOT NULL DEFAULT 50,
    start_row      INT  NOT NULL,
    start_col      INT  NOT NULL,
    treasure_row   INT  NOT NULL,
    treasure_col   INT  NOT NULL,
    path           JSONB NOT NULL, -- ordered [{"row":r,"col":c}, ...] start..treasure inclusive
    active         BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dungeons_active ON dungeons(active, created_at DESC);

CREATE TABLE IF NOT EXISTS dungeon_monsters (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dungeon_id   UUID NOT NULL REFERENCES dungeons(id) ON DELETE CASCADE,
    cell_row     INT  NOT NULL,
    cell_col     INT  NOT NULL,
    image_id     UUID NOT NULL REFERENCES images(id),
    grid_cols    INT  NOT NULL DEFAULT 3,
    grid_rows    INT  NOT NULL DEFAULT 3,
    play_seconds INT  NOT NULL DEFAULT 120
);

CREATE INDEX IF NOT EXISTS idx_dungeon_monsters_dungeon ON dungeon_monsters(dungeon_id);

-- Per-user saved position/state within a dungeon. Lazily created on the
-- player's first move (see repository.MoveDungeon).
CREATE TABLE IF NOT EXISTS dungeon_progress (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dungeon_id   UUID NOT NULL REFERENCES dungeons(id) ON DELETE CASCADE,
    pos_row      INT  NOT NULL,
    pos_col      INT  NOT NULL,
    prev_row     INT,
    prev_col     INT,
    defeated     JSONB NOT NULL DEFAULT '[]', -- array of monster uuid strings
    visited      JSONB NOT NULL DEFAULT '[]', -- array of {"row","col"} for fog-of-war restore
    completed    BOOLEAN NOT NULL DEFAULT false,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, dungeon_id)
);
