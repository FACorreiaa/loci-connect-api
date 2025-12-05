-- +goose Up
-- Table for user favorite restaurants
CREATE TABLE user_favorite_restaurants (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES restaurant_details(id) ON DELETE CASCADE,
    notes TEXT NULL,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_user_restaurant UNIQUE (user_id, restaurant_id)
);

-- Indexes for user_favorite_restaurants
CREATE INDEX idx_user_favorite_restaurants_user_id ON user_favorite_restaurants(user_id);
CREATE INDEX idx_user_favorite_restaurants_restaurant_id ON user_favorite_restaurants(restaurant_id);
CREATE INDEX idx_user_favorite_restaurants_added_at ON user_favorite_restaurants(added_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_user_favorite_restaurants_added_at;
DROP INDEX IF EXISTS idx_user_favorite_restaurants_restaurant_id;
DROP INDEX IF EXISTS idx_user_favorite_restaurants_user_id;
DROP TABLE IF EXISTS user_favorite_restaurants;
