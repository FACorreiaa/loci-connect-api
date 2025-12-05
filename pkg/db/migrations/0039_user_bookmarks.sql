-- +goose Up

-- Create user_bookmarked_itineraries table to track bookmarked itineraries
CREATE TABLE user_bookmarked_itineraries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    itinerary_id UUID NOT NULL REFERENCES user_saved_itineraries(id) ON DELETE CASCADE,
    bookmarked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Ensure a user can only bookmark an itinerary once
    UNIQUE(user_id, itinerary_id)
);

-- Indexes for performance
CREATE INDEX idx_user_bookmarked_itineraries_user_id ON user_bookmarked_itineraries(user_id);
CREATE INDEX idx_user_bookmarked_itineraries_itinerary_id ON user_bookmarked_itineraries(itinerary_id);
CREATE INDEX idx_user_bookmarked_itineraries_bookmarked_at ON user_bookmarked_itineraries(bookmarked_at DESC);

-- Composite index for common queries
CREATE INDEX idx_user_bookmarked_itineraries_user_bookmarked ON user_bookmarked_itineraries(user_id, bookmarked_at DESC);

-- +goose Down

DROP TABLE IF EXISTS user_bookmarked_itineraries;
