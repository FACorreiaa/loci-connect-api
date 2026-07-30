-- +goose Up
-- +goose StatementBegin

-- Multi-city trips: a trip can now span any number of cities across any number
-- of days. "Two cities in a weekend" is the smallest case of this, not a
-- separate feature.
--
-- Design note: the city lives on the DAY rather than on the trip, because that
-- is the granularity a traveller edits at ("spend Thursday in Coimbra instead").
-- trips.city_name stays as the PRIMARY city — titles, exports and every
-- single-city trip written before this migration keep working untouched, and a
-- NULL/empty day city means "the trip's city".
ALTER TABLE trip_days
    ADD COLUMN IF NOT EXISTS city_id    UUID,
    ADD COLUMN IF NOT EXISTS city_name  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS city_lat   DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS city_lon   DOUBLE PRECISION,
    -- A day that includes a move between cities has less time for sightseeing;
    -- the UI says so rather than silently overbooking it.
    ADD COLUMN IF NOT EXISTS travel_day BOOLEAN NOT NULL DEFAULT FALSE;

-- Travel between consecutive cities. Kept as its own table rather than JSONB on
-- the trip so legs can be queried and, later, priced or booked individually.
--
-- after_day is the day number at whose END the journey happens; 0 is the
-- outbound leg from home before day 1. It is a number rather than an FK to
-- trip_days so a leg survives a day being renumbered mid-edit.
CREATE TABLE IF NOT EXISTS trip_legs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id       UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    after_day     INTEGER NOT NULL DEFAULT 0 CHECK (after_day >= 0),
    from_name     TEXT NOT NULL DEFAULT '',
    to_name       TEXT NOT NULL DEFAULT '',
    from_lat      DOUBLE PRECISION,
    from_lon      DOUBLE PRECISION,
    to_lat        DOUBLE PRECISION,
    to_lon        DOUBLE PRECISION,
    distance_km   DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (distance_km >= 0),
    duration_mins INTEGER NOT NULL DEFAULT 0 CHECK (duration_mins >= 0),
    mode          TEXT NOT NULL DEFAULT 'drive',
    booking_url   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trip_legs_trip_after_day ON trip_legs (trip_id, after_day);

-- Finding "which days am I in city X" is the read the multi-city timeline does
-- on every render.
CREATE INDEX IF NOT EXISTS idx_trip_days_city ON trip_days (trip_id, city_name);

-- +goose StatementEnd
