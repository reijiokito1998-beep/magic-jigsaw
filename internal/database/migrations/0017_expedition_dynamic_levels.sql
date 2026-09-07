-- Expedition is no longer a fixed 100-level campaign: an admin can append new
-- levels from the management screen. The original CHECK pinned level_index to
-- 1..100, which would reject every level created past the seeded 100, so it is
-- replaced by a lower bound only. The campaign length is now whatever
-- MAX(level_index) says (see repository.ExpeditionLevelCount).
ALTER TABLE expedition_levels
    DROP CONSTRAINT IF EXISTS expedition_levels_level_index_check;

ALTER TABLE expedition_levels
    ADD CONSTRAINT expedition_levels_level_index_check CHECK (level_index >= 1);
