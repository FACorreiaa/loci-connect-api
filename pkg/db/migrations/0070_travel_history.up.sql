-- +goose Up
-- +goose StatementBegin

-- Travel history: where a traveller has actually been.
--
-- This replaces two things that were not real:
--   * statistics.visited_cities_count, computed as `hotels + restaurants` and
--     commented "Placeholder" (internal/domain/statistics/handler.go).
--   * users.places_visited, a denormalised integer with no backing rows, which
--     could only ever drift away from the truth.
--
-- A visited place is a ROW here, with provenance, so any number derived from it
-- can be explained, audited and undone. That is the whole point: a counter can
-- be wrong silently, a table cannot.

CREATE TABLE IF NOT EXISTS user_visited_cities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- Nullable: a trip can name a city we hold no `cities` row for yet. We would
    -- rather record "Porto, coordinates known, id unknown" than drop the visit.
    city_id UUID REFERENCES cities (id) ON DELETE SET NULL,
    city_name TEXT NOT NULL CHECK (city_name <> ''),

    -- Empty when unresolved. Deliberately never inferred from coordinates: that
    -- needs a reverse geocode we do not have, and a guessed country is exactly
    -- the class of fabricated data this table exists to remove.
    country TEXT NOT NULL DEFAULT '',
    country_code CHAR(2),

    -- Required. The globe plots these directly, so a city we cannot place is not
    -- recorded rather than being silently placed at 0,0 off the coast of Africa.
    latitude DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),

    -- How we came to know about this visit: trip | visit_event | manual | backfill.
    source TEXT NOT NULL DEFAULT 'manual'
        CHECK (source IN ('trip', 'visit_event', 'manual', 'backfill')),
    trip_id UUID REFERENCES trips (id) ON DELETE SET NULL,

    first_visit_at TIMESTAMPTZ NOT NULL,
    last_visit_at TIMESTAMPTZ NOT NULL,
    visit_count INTEGER NOT NULL DEFAULT 1 CHECK (visit_count >= 1),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_user_visited_cities_order CHECK (last_visit_at >= first_visit_at)
);

-- One row per (user, city). Two partial indexes rather than one, because a
-- resolved city dedupes on its id while an unresolved one can only dedupe on a
-- normalised name — otherwise "Porto", "porto" and "Porto " stack up as three
-- separate places the user has been.
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_visited_cities_city
    ON user_visited_cities (user_id, city_id)
    WHERE city_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_visited_cities_name
    ON user_visited_cities (user_id, LOWER(BTRIM(city_name)), LOWER(BTRIM(country)))
    WHERE city_id IS NULL;

-- The globe and the history list both read "this user's cities, most recent
-- first"; the stats rail groups by country.
CREATE INDEX IF NOT EXISTS idx_user_visited_cities_user_last
    ON user_visited_cities (user_id, last_visit_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_visited_cities_country
    ON user_visited_cities (user_id, country);

-- Per-POI visits. This gives the "Mark visited" action a home, and means the
-- city-level roll-up above can be RECOMPUTED from evidence rather than trusted.
CREATE TABLE IF NOT EXISTS user_visited_pois (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- TEXT, not UUID: POI ids follow the same convention as
    -- recommendation_events.poi_id, which is TEXT.
    poi_id TEXT NOT NULL CHECK (poi_id <> ''),
    poi_name TEXT NOT NULL DEFAULT '',
    city_name TEXT NOT NULL DEFAULT '',

    -- Optional here, unlike cities: a POI we hold no coordinates for is still a
    -- real visit, it just does not get its own dot on the globe.
    latitude DOUBLE PRECISION CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    longitude DOUBLE PRECISION CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180),

    trip_id UUID REFERENCES trips (id) ON DELETE SET NULL,
    source TEXT NOT NULL DEFAULT 'visit_event'
        CHECK (source IN ('trip', 'visit_event', 'manual', 'backfill')),
    visited_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Visiting the same place twice on different days is two visits; visiting it
    -- twice at the same instant is one event replayed.
    UNIQUE (user_id, poi_id, visited_at)
);

CREATE INDEX IF NOT EXISTS idx_user_visited_pois_user_visited
    ON user_visited_pois (user_id, visited_at DESC);

-- Records that the one-time backfill over pre-existing signals (visit-confirmed
-- events, dated trip days) has run for a user. Without this the backfill either
-- runs on every read or never runs for users who existed before the feature.
CREATE TABLE IF NOT EXISTS travel_history_backfills (
    user_id UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    version INTEGER NOT NULL DEFAULT 1,
    ran_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS travel_history_backfills;
DROP TABLE IF EXISTS user_visited_pois;
DROP TABLE IF EXISTS user_visited_cities;
-- +goose StatementEnd
