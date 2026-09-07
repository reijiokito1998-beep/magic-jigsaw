-- Expedition stars sold as an in-app purchase (expedition_stars_50).
--
-- Until now a user's star total was fully derived from play (completed levels +
-- completed daily challenges), so there was nowhere to record stars that were
-- bought rather than earned. This column holds exactly those purchased stars
-- and is added on top of the derived total by
-- repository.TotalExpeditionStars -- it never changes what a level awards.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS expedition_bonus_stars INT NOT NULL DEFAULT 0;

-- The purchase ledger records what was actually paid out, so a star purchase
-- has to be representable there too. Like points_credited this is stored
-- rather than derived from product_id, so past rows stay truthful when the
-- catalogue changes.
ALTER TABLE purchases
    ADD COLUMN IF NOT EXISTS stars_credited INT NOT NULL DEFAULT 0;
