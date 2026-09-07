-- Make each expedition level's game config editable by admins. Previously the
-- tier (star/grid/time/category/XP) was derived from the level index in code;
-- these nullable columns let an admin override any field per level. A NULL
-- column means "use the code-derived tier default" for that field.
ALTER TABLE expedition_levels
    ADD COLUMN IF NOT EXISTS star         INT,
    ADD COLUMN IF NOT EXISTS grid_cols    INT,
    ADD COLUMN IF NOT EXISTS grid_rows    INT,
    ADD COLUMN IF NOT EXISTS play_seconds INT,
    ADD COLUMN IF NOT EXISTS category     TEXT,
    ADD COLUMN IF NOT EXISTS xp_reward    INT;
