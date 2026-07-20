-- +goose Up
-- +goose StatementBegin
-- Editable day-by-day trips (Slice 2). Replaces the flat itinerary_pois model
-- for user-facing trip planning; user_saved_itineraries markdown stays as a
-- legacy read-only store.
CREATE TABLE IF NOT EXISTS trips (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    city_id           UUID,
    city_name         TEXT NOT NULL DEFAULT '',
    title             TEXT NOT NULL DEFAULT 'Untitled Trip',
    constraints       JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Monotonic version for optimistic concurrency / merge-safe multi-device edits.
    version           BIGINT NOT NULL DEFAULT 0,
    source_session_id TEXT,
    is_public         BOOLEAN NOT NULL DEFAULT FALSE,
    share_code        TEXT UNIQUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trip_days (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    day_number INTEGER NOT NULL CHECK (day_number >= 1),
    date       TIMESTAMPTZ,
    UNIQUE (trip_id, day_number)
);

CREATE TABLE IF NOT EXISTS trip_stops (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    day_id           UUID NOT NULL REFERENCES trip_days (id) ON DELETE CASCADE,
    poi_id           TEXT NOT NULL DEFAULT '',
    order_index      INTEGER NOT NULL DEFAULT 0 CHECK (order_index >= 0),
    name             TEXT NOT NULL DEFAULT '',
    start_minute     INTEGER CHECK (start_minute BETWEEN 0 AND 1440),
    duration_minutes INTEGER CHECK (duration_minutes >= 0),
    notes            TEXT NOT NULL DEFAULT '',
    booking_url      TEXT
);

-- Immutable per-version copies so a device can reconcile against a known base.
CREATE TABLE IF NOT EXISTS trip_snapshots (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    version    BIGINT NOT NULL,
    data       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (trip_id, version)
);

CREATE INDEX IF NOT EXISTS idx_trips_user_id ON trips (user_id);
CREATE INDEX IF NOT EXISTS idx_trip_days_trip_id ON trip_days (trip_id);
CREATE INDEX IF NOT EXISTS idx_trip_stops_day_order ON trip_stops (day_id, order_index);
CREATE INDEX IF NOT EXISTS idx_trip_snapshots_trip_id ON trip_snapshots (trip_id, version);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_snapshots;
DROP TABLE IF EXISTS trip_stops;
DROP TABLE IF EXISTS trip_days;
DROP TABLE IF EXISTS trips;
-- +goose StatementEnd
