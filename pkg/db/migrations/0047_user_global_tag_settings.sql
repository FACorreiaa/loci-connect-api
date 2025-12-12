-- +goose Up
-- +goose StatementBegin

-- Create table to store user-specific settings for global tags
-- This allows users to toggle global tags on/off without modifying the global tags themselves
CREATE TABLE user_global_tag_settings (
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    global_tag_id UUID NOT NULL REFERENCES global_tags (id) ON DELETE CASCADE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP
    WITH
        TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP
    WITH
        TIME ZONE,
        PRIMARY KEY (user_id, global_tag_id)
);

CREATE INDEX idx_user_global_tag_settings_user_id ON user_global_tag_settings (user_id);

CREATE INDEX idx_user_global_tag_settings_global_tag_id ON user_global_tag_settings (global_tag_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_global_tag_settings;
-- +goose StatementEnd