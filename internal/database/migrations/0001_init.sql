-- Initial schema for the Jigsaw backend.
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Users authenticated via Google Sign-In.
CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    google_sub TEXT UNIQUE NOT NULL,
    email      TEXT UNIQUE NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    role       TEXT NOT NULL DEFAULT 'user', -- 'user' | 'admin'
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Image assets stored on Cloudinary. Used both for user uploads and for the
-- admin's daily challenge images.
CREATE TABLE IF NOT EXISTS images (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id    TEXT NOT NULL,                 -- Cloudinary public_id
    url          TEXT NOT NULL,                 -- Cloudinary secure_url
    width        INT  NOT NULL DEFAULT 0,
    height       INT  NOT NULL DEFAULT 0,
    title        TEXT NOT NULL DEFAULT '',
    uploaded_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    is_challenge BOOLEAN NOT NULL DEFAULT false, -- true if uploaded as a daily challenge
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One challenge per calendar day, created by an admin.
CREATE TABLE IF NOT EXISTS daily_challenges (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge_date DATE NOT NULL UNIQUE,
    image_id       UUID NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    play_seconds   INT  NOT NULL,               -- time limit to complete
    title          TEXT NOT NULL DEFAULT '',
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A user's saved image collection (many-to-many user <-> image).
CREATE TABLE IF NOT EXISTS collection_items (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    image_id UUID NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, image_id)
);

-- A user's reputation ("Danh vọng") for each daily challenge.
CREATE TABLE IF NOT EXISTS challenge_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_id    UUID NOT NULL REFERENCES daily_challenges(id) ON DELETE CASCADE,
    status          TEXT NOT NULL,              -- 'in_progress' | 'completed' | 'failed'
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,
    elapsed_seconds INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, challenge_id)
);

CREATE INDEX IF NOT EXISTS idx_collection_user ON collection_items(user_id);
CREATE INDEX IF NOT EXISTS idx_results_user ON challenge_results(user_id);
CREATE INDEX IF NOT EXISTS idx_challenges_date ON daily_challenges(challenge_date DESC);
