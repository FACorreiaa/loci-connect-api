-- +goose Up
CREATE TABLE IF NOT EXISTS location_history (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    radius DOUBLE PRECISION NOT NULL DEFAULT 5.0,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_location_history_user_id ON location_history(user_id);
CREATE INDEX IF NOT EXISTS idx_location_history_timestamp ON location_history(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_location_history_user_timestamp ON location_history(user_id, timestamp DESC);

-- Create composite index for geospatial queries
CREATE INDEX IF NOT EXISTS idx_location_history_coordinates ON location_history(latitude, longitude);

-- Add comment
COMMENT ON TABLE location_history IS 'Stores user location history for the nearby feature';

-- +goose Down
DROP INDEX IF EXISTS idx_location_history_coordinates;
DROP INDEX IF EXISTS idx_location_history_user_timestamp;
DROP INDEX IF EXISTS idx_location_history_timestamp;
DROP INDEX IF EXISTS idx_location_history_user_id;
DROP TABLE IF EXISTS location_history;
