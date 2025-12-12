-- +goose Up
-- SQL in this section is executed when the migration is applied.

CREATE TABLE IF NOT EXISTS user_global_interest_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    global_interest_id UUID NOT NULL REFERENCES interests (id) ON DELETE CASCADE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_global_interest UNIQUE (user_id, global_interest_id)
);

CREATE INDEX IF NOT EXISTS idx_user_global_interest_settings_user_id ON user_global_interest_settings (user_id);

CREATE INDEX IF NOT EXISTS idx_user_global_interest_settings_global_interest_id ON user_global_interest_settings (global_interest_id);

COMMENT ON
TABLE user_global_interest_settings IS 'Stores user-specific active/inactive state for global interests';

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.

DROP TABLE IF EXISTS user_global_interest_settings;