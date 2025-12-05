-- +goose Up
-- Table for user favorite hotels
CREATE TABLE user_favorite_hotels (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hotel_id UUID NOT NULL REFERENCES hotel_details(id) ON DELETE CASCADE,
    notes TEXT NULL,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_hotel UNIQUE (user_id, hotel_id)
);

-- Indexes for user_favorite_hotels
CREATE INDEX idx_user_favorite_hotels_user_id ON user_favorite_hotels(user_id);
CREATE INDEX idx_user_favorite_hotels_hotel_id ON user_favorite_hotels(hotel_id);
CREATE INDEX idx_user_favorite_hotels_added_at ON user_favorite_hotels(added_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_user_favorite_hotels_added_at;
DROP INDEX IF EXISTS idx_user_favorite_hotels_hotel_id;
DROP INDEX IF EXISTS idx_user_favorite_hotels_user_id;
DROP TABLE IF EXISTS user_favorite_hotels;
