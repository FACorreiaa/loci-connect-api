-- +goose Up
-- +goose StatementBegin
-- Align auth schema with application expectations.

-- 1) Add missing columns on users table that the code expects.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS hashed_password VARCHAR(255),
    ADD COLUMN IF NOT EXISTS avatar_url TEXT;

-- Backfill from legacy columns when possible to avoid breaking existing users.
UPDATE users
SET hashed_password = COALESCE(hashed_password, password_hash),
    avatar_url = COALESCE(avatar_url, profile_image_url)
WHERE (password_hash IS NOT NULL OR profile_image_url IS NOT NULL);

-- 2) Create user_sessions table used for refresh token session tracking.
CREATE TABLE IF NOT EXISTS user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hashed_refresh_token TEXT UNIQUE NOT NULL,
    user_agent TEXT,
    client_ip TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at ON user_sessions(expires_at);

-- 3) Create user_tokens table used for email verification and password reset flows.
CREATE TABLE IF NOT EXISTS user_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id ON user_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id_type ON user_tokens(user_id, type);
-- +goose StatementEnd
