-- Splits the campaign in two: levels shown on the Expedition map, and levels
-- that only exist inside a themed collection (Pokemon / Animal / Gundam)
-- reached through the black hole portal.
--
-- Until now `category` did double duty — it is the tier's display name on the
-- map ("Abyss of Ruin", ...) *and* the client's collection filter, so a level
-- categorised "pokemon" showed up in both places at once. With this flag the
-- two roles separate cleanly: show_on_main = false means the level is off the
-- map, which frees its `category` to be the collection slug.
--
-- Collection levels are always playable regardless of stars, and clearing one
-- still pays its stars into the shared total (repository.TotalExpeditionStars),
-- so a collection run helps unlock map levels. They are skipped only when
-- computing what a map level *costs* (models.RequiredStarsFromTiers), since a
-- level that is off the map must not raise the bar for the ones after it.
--
-- Defaults to TRUE so every level that exists today keeps its place on the map.
ALTER TABLE expedition_levels
    ADD COLUMN IF NOT EXISTS show_on_main BOOLEAN NOT NULL DEFAULT TRUE;
