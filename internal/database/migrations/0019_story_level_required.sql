-- Gate a story behind the player's level: the story only becomes playable once
-- the user reaches level_required. 1 means "open to everyone", which is what
-- every existing story keeps, so this change unlocks nothing and locks nothing
-- until an admin raises it.
ALTER TABLE stories
    ADD COLUMN IF NOT EXISTS level_required INT NOT NULL DEFAULT 1;
