-- Create poi_detailed_info table to store structured POI results
-- +goose Up
-- SQL in section 'Up' is executed when this migration is applied
CREATE TABLE IF NOT EXISTS poi_detailed_info (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_name TEXT,
    city_id UUID,
    name TEXT NOT NULL,
    description_poi TEXT,
    distance DOUBLE PRECISION,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    category TEXT,
    description TEXT,
    rating DOUBLE PRECISION,
    address TEXT,
    phone_number TEXT,
    website TEXT,
    opening_hours JSONB,
    images TEXT[],
    price_range TEXT,
    price_level TEXT,
    reviews TEXT[],
    llm_interaction_id UUID,
    tags TEXT[],
    priority INT,
    amenities TEXT,
    cuisine_type TEXT,
    star_rating TEXT,
    source TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Helpful indexes for discover queries
CREATE INDEX IF NOT EXISTS idx_poi_detailed_info_category ON poi_detailed_info (LOWER(category));
CREATE INDEX IF NOT EXISTS idx_poi_detailed_info_city_name ON poi_detailed_info (LOWER(city_name));
CREATE INDEX IF NOT EXISTS idx_poi_detailed_info_rating ON poi_detailed_info (rating);

-- +goose Down
-- SQL section 'Down' is executed when this migration is rolled back
DROP INDEX IF EXISTS idx_poi_detailed_info_rating;
DROP INDEX IF EXISTS idx_poi_detailed_info_city_name;
DROP INDEX IF EXISTS idx_poi_detailed_info_category;
DROP TABLE IF EXISTS poi_detailed_info;
