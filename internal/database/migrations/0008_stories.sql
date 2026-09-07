-- Story Library: a story is an ordered set of image "pages". Each page is a
-- jigsaw puzzle; completing a page unlocks the next one (sequential).

CREATE TABLE IF NOT EXISTS stories (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title          TEXT NOT NULL DEFAULT '',
    category       TEXT NOT NULL DEFAULT '',
    author         TEXT NOT NULL DEFAULT '',
    description    TEXT NOT NULL DEFAULT '',
    cover_image_id UUID REFERENCES images(id) ON DELETE SET NULL,
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS story_pages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    story_id    UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    page_index  INT  NOT NULL,
    image_id    UUID NOT NULL REFERENCES images(id),
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    grid_cols   INT  NOT NULL DEFAULT 4,
    grid_rows   INT  NOT NULL DEFAULT 6,
    xp_reward   INT  NOT NULL DEFAULT 100,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (story_id, page_index)
);

CREATE INDEX IF NOT EXISTS idx_story_pages_story ON story_pages(story_id);

-- Per-user completion of individual story pages (drives sequential unlock).
CREATE TABLE IF NOT EXISTS story_page_progress (
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    page_id      UUID NOT NULL REFERENCES story_pages(id) ON DELETE CASCADE,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, page_id)
);
