-- +goose Up
-- +goose StatementBegin
-- A trip's plan: its dates, where it sleeps and its flights. Day dates stay on
-- trip_days.date (the calendar and ICS feed read them there); start_date and
-- end_date are what the traveller chose, which a 3-day plan inside a 5-day
-- trip needs to keep.
ALTER TABLE trips
    ADD COLUMN IF NOT EXISTS start_date DATE NULL,
    ADD COLUMN IF NOT EXISTS end_date   DATE NULL;
ALTER TABLE trips
    ADD CONSTRAINT trips_dates_ordered
    CHECK (start_date IS NULL OR end_date IS NULL OR end_date >= start_date);

-- One stay per city, matched case-insensitively: "Lisbon" and "lisbon " are
-- the same city to a traveller. poi_id is empty when the hotel came from a
-- generated list with no stored POI.
CREATE TABLE IF NOT EXISTS trip_stays (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id     UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    city_name   TEXT NOT NULL,
    poi_id      TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL,
    star_rating TEXT NOT NULL DEFAULT '',
    check_in    DATE NULL,
    check_out   DATE NULL,
    booking_url TEXT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_trip_stays_trip_city
    ON trip_stays (trip_id, lower(btrim(city_name)));

-- Flights the traveller chose. links is server-built JSON
-- ([{provider,label,url}]); price_text is whatever the traveller typed and is
-- never a quoted fare. ids are assigned by the server and kept across saves so
-- RemoveFlight can name one.
CREATE TABLE IF NOT EXISTS trip_flights (
    id               UUID PRIMARY KEY,
    trip_id          UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    origin_name      TEXT NOT NULL,
    origin_iata      TEXT NOT NULL DEFAULT '',
    destination_name TEXT NOT NULL,
    destination_iata TEXT NOT NULL DEFAULT '',
    depart_date      DATE NOT NULL,
    return_date      DATE NULL,
    passengers       INT  NOT NULL DEFAULT 1,
    cabin            INT  NOT NULL DEFAULT 0,
    links            JSONB NOT NULL DEFAULT '[]',
    carrier          TEXT NULL,
    flight_no        TEXT NULL,
    price_text       TEXT NULL,
    notes            TEXT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trip_flights_trip ON trip_flights (trip_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_flights;
DROP TABLE IF EXISTS trip_stays;
ALTER TABLE trips DROP CONSTRAINT IF EXISTS trips_dates_ordered;
ALTER TABLE trips
    DROP COLUMN IF EXISTS end_date,
    DROP COLUMN IF EXISTS start_date;
-- +goose StatementEnd
