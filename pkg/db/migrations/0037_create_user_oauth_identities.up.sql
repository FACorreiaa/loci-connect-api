-- +goose Up
-- +goose StatementBegin
-- Create user_oauth_identities table for OAuth provider linkage.
CREATE TABLE IF NOT EXISTS user_oauth_identities (
    provider_name TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_access_token TEXT,
    provider_refresh_token TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider_name, provider_user_id)
);

CREATE INDEX IF NOT EXISTS idx_user_oauth_identities_user_id ON user_oauth_identities(user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_oauth_identities;
-- +goose StatementEnd
