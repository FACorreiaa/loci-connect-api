-- +goose Up
-- +goose StatementBegin
-- Where a stay is, and what the traveller said it costs.
--
-- A stay had a name and a city and nowhere to stand: enough to list it, not
-- enough to say which metro stop is round the corner or how far the first
-- stop of the day is. lat/lon/address come from Loci's own hotel and map
-- data: a hotel picked from search, or one a comparison report identified.
--
-- price_note is the traveller's own words ("385 euros with breakfast"). A
-- rate read from a booking partner is never stored here.
--
-- NULL on every existing stay: those have no transport section until the
-- traveller picks the hotel again.
ALTER TABLE trip_stays
    ADD COLUMN IF NOT EXISTS lat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS lon DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS address TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS price_note TEXT;

ALTER TABLE trip_stays
    ADD CONSTRAINT trip_stays_pin_chk
    CHECK (
        (lat IS NULL AND lon IS NULL)
        OR (lat BETWEEN -90 AND 90 AND lon BETWEEN -180 AND 180)
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE trip_stays
    DROP CONSTRAINT IF EXISTS trip_stays_pin_chk,
    DROP COLUMN IF EXISTS price_note,
    DROP COLUMN IF EXISTS address,
    DROP COLUMN IF EXISTS lon,
    DROP COLUMN IF EXISTS lat;
-- +goose StatementEnd
