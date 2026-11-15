-- +goose Up
-- +goose StatementBegin
-- A trip stop's own pin.
--
-- A stop used to get its position only from the place behind poi_id, looked up
-- when the trip is read. A place the traveller pastes from a map link that
-- Loci does not know has no poi_id, so it had a name and nowhere to stand on
-- the map. Unverified places stay out of points_of_interest (see
-- 0094_place_submissions), so the position lives on the stop instead.
--
-- NULL on every existing stop and on any stop with a poi_id that the client
-- sent no pin for: those keep reading their position from the POI.
ALTER TABLE trip_stops
    ADD COLUMN IF NOT EXISTS lat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS lon DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS address TEXT;

ALTER TABLE trip_stops
    ADD CONSTRAINT trip_stops_pin_chk
    CHECK (
        (lat IS NULL AND lon IS NULL)
        OR (lat BETWEEN -90 AND 90 AND lon BETWEEN -180 AND 180)
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE trip_stops
    DROP CONSTRAINT IF EXISTS trip_stops_pin_chk,
    DROP COLUMN IF EXISTS address,
    DROP COLUMN IF EXISTS lon,
    DROP COLUMN IF EXISTS lat;
-- +goose StatementEnd
