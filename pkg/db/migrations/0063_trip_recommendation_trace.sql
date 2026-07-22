-- +goose Up
ALTER TABLE trip_stops
    ADD COLUMN IF NOT EXISTS recommendation_trace JSONB;

-- +goose Down
ALTER TABLE trip_stops
    DROP COLUMN IF EXISTS recommendation_trace;
