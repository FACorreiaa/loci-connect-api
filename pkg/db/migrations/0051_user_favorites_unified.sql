-- +goose Up
-- +goose StatementBegin

-- Unified favorites table for all content types
CREATE TABLE IF NOT EXISTS user_favorites (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4 (),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    item_id TEXT NOT NULL,
    item_name TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT 'poi', -- poi, hotel, restaurant, itinerary
    notes TEXT DEFAULT '',
    description TEXT DEFAULT '',
    city_name TEXT DEFAULT '',
    latitude DOUBLE PRECISION DEFAULT 0,
    longitude DOUBLE PRECISION DEFAULT 0,
    rating DOUBLE PRECISION DEFAULT 0,
    category TEXT DEFAULT '',
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_item_type UNIQUE (
        user_id,
        item_id,
        content_type
    )
);

-- Indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_user_favorites_user_id ON user_favorites (user_id);

CREATE INDEX IF NOT EXISTS idx_user_favorites_content_type ON user_favorites (user_id, content_type);

CREATE INDEX IF NOT EXISTS idx_user_favorites_added_at ON user_favorites (added_at DESC);

-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS idx_user_favorites_added_at;

DROP INDEX IF EXISTS idx_user_favorites_content_type;

DROP INDEX IF EXISTS idx_user_favorites_user_id;

DROP TABLE IF EXISTS user_favorites;