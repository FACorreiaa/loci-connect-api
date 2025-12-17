-- +goose Up
-- Password Reset Tokens Table
-- Used for secure password reset flow

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token VARCHAR(128) NOT NULL UNIQUE,
    expires_at TIMESTAMP
    WITH
        TIME ZONE NOT NULL,
        used_at TIMESTAMP
    WITH
        TIME ZONE,
        created_at TIMESTAMP
    WITH
        TIME ZONE DEFAULT NOW()
);

-- Index for token lookup
CREATE INDEX idx_password_reset_tokens_token ON password_reset_tokens (token);

-- Index for cleanup of expired tokens
CREATE INDEX idx_password_reset_tokens_expires_at ON password_reset_tokens (expires_at);

-- Index for user lookup (to invalidate old tokens)
CREATE INDEX idx_password_reset_tokens_user_id ON password_reset_tokens (user_id);

COMMENT ON
TABLE password_reset_tokens IS 'Stores password reset tokens for secure password recovery';

COMMENT ON COLUMN password_reset_tokens.token IS 'Secure random token (64 hex chars)';

COMMENT ON COLUMN password_reset_tokens.expires_at IS 'Token expiry time (typically 1 hour)';

COMMENT ON COLUMN password_reset_tokens.used_at IS 'When the token was used (NULL if unused)';

-- +goose Down
DROP TABLE IF EXISTS password_reset_tokens;