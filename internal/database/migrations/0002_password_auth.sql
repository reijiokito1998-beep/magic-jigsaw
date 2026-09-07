-- Switch from Google Sign-In to email/password auth.
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT '';

-- google_sub is no longer required (kept nullable for any legacy rows).
ALTER TABLE users ALTER COLUMN google_sub DROP NOT NULL;
