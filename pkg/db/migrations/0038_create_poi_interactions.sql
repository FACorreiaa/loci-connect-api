-- +goose Up
CREATE TABLE IF NOT EXISTS poi_interactions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    poi_id VARCHAR(255) NOT NULL,
    poi_name VARCHAR(500) NOT NULL,
    poi_category VARCHAR(100) NOT NULL,
    interaction_type VARCHAR(50) NOT NULL CHECK (interaction_type IN ('view', 'click', 'favorite')),
    user_latitude DOUBLE PRECISION NOT NULL,
    user_longitude DOUBLE PRECISION NOT NULL,
    poi_latitude DOUBLE PRECISION NOT NULL,
    poi_longitude DOUBLE PRECISION NOT NULL,
    distance DOUBLE PRECISION NOT NULL,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_poi_interactions_user_id ON poi_interactions(user_id);
CREATE INDEX IF NOT EXISTS idx_poi_interactions_poi_id ON poi_interactions(poi_id);
CREATE INDEX IF NOT EXISTS idx_poi_interactions_timestamp ON poi_interactions(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_poi_interactions_user_timestamp ON poi_interactions(user_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_poi_interactions_category ON poi_interactions(poi_category);
CREATE INDEX IF NOT EXISTS idx_poi_interactions_type ON poi_interactions(interaction_type);
CREATE INDEX IF NOT EXISTS idx_poi_interactions_user_category ON poi_interactions(user_id, poi_category);

-- Add comment
COMMENT ON TABLE poi_interactions IS 'Stores user interactions with Points of Interest for analytics';

-- +goose Down
DROP INDEX IF EXISTS idx_poi_interactions_user_category;
DROP INDEX IF EXISTS idx_poi_interactions_type;
DROP INDEX IF EXISTS idx_poi_interactions_category;
DROP INDEX IF EXISTS idx_poi_interactions_user_timestamp;
DROP INDEX IF EXISTS idx_poi_interactions_timestamp;
DROP INDEX IF EXISTS idx_poi_interactions_poi_id;
DROP INDEX IF EXISTS idx_poi_interactions_user_id;
DROP TABLE IF EXISTS poi_interactions;
