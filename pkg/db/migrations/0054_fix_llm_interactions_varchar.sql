-- +goose Up
-- Convert remaining VARCHAR columns in llm_interactions to TEXT
-- This fixes TimescaleDB hypertable warnings about column types

ALTER TABLE llm_interactions
    ALTER COLUMN provider TYPE TEXT,
    ALTER COLUMN intent TYPE TEXT,
    ALTER COLUMN search_type TYPE TEXT,
    ALTER COLUMN device_type TYPE TEXT,
    ALTER COLUMN platform TYPE TEXT,
    ALTER COLUMN prompt_hash TYPE TEXT;

-- +goose Down
ALTER TABLE llm_interactions
    ALTER COLUMN provider TYPE VARCHAR(50),
    ALTER COLUMN intent TYPE VARCHAR(100),
    ALTER COLUMN search_type TYPE VARCHAR(50),
    ALTER COLUMN device_type TYPE VARCHAR(50),
    ALTER COLUMN platform TYPE VARCHAR(50),
    ALTER COLUMN prompt_hash TYPE VARCHAR(64);