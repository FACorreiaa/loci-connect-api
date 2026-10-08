-- +goose Up
-- +goose StatementBegin
-- The field score: points only for exploration a traveller can show they did,
-- counted per city and per ISO week, with a permanent lifetime rank.
--
-- The ledger stays the source of truth. points keeps what each row was
-- awarded with, so history still reads "+50 New city"; field_points is what
-- the row counts under the field score, and every field total sums it. Daily
-- check-ins (kind 1) and first searches (kind 2) are retired: their rows stay
-- and count 0.
ALTER TABLE points_events DROP CONSTRAINT IF EXISTS points_events_kind_check;
ALTER TABLE points_events
    ADD CONSTRAINT points_events_kind_check CHECK (kind BETWEEN 1 AND 13);

-- city_id carries no foreign key: trips.city_id never had one either, and a
-- city row going away must not take a ledger row with it.
ALTER TABLE points_events
    ADD COLUMN IF NOT EXISTS city_id UUID,
    ADD COLUMN IF NOT EXISTS season_id INTEGER,
    ADD COLUMN IF NOT EXISTS field_points INTEGER NOT NULL DEFAULT 0 CHECK (field_points >= 0);

-- A season is the ISO week of the traveller's own local date, written at
-- insert so a board never re-derives it differently: 2026-10-08 is 202641.
UPDATE points_events
SET season_id = EXTRACT(isoyear FROM local_date)::int * 100 + EXTRACT(week FROM local_date)::int
WHERE season_id IS NULL;
ALTER TABLE points_events ALTER COLUMN season_id SET NOT NULL;

-- Which city each existing row happened in, where that can be known.
UPDATE points_events pe SET city_id = vc.city_id
FROM user_visited_cities vc
WHERE pe.kind = 4 AND pe.ref_key = 'city:' || vc.id::text AND vc.city_id IS NOT NULL;

UPDATE points_events pe SET city_id = p.city_id
FROM points_of_interest p
WHERE pe.kind = 3 AND p.id::text = split_part(pe.ref_key, ':', 2) AND p.city_id IS NOT NULL;

UPDATE points_events pe SET city_id = p.city_id
FROM place_claims pc JOIN points_of_interest p ON p.id::text = pc.poi_id
WHERE pe.kind = 5 AND pe.ref_key = 'claim:' || pc.id::text AND p.city_id IS NOT NULL;

UPDATE points_events pe SET city_id = COALESCE(d.city_id, t.city_id)
FROM trip_days d JOIN trips t ON t.id = d.trip_id
WHERE pe.kind = 7 AND pe.ref_key = 'tripday:' || d.id::text;

UPDATE points_events pe SET city_id = t.city_id
FROM trips t
WHERE pe.kind = 8 AND pe.ref_key = 'trip:' || t.id::text;

-- Rescore under the field economy.
UPDATE points_events SET field_points = CASE kind
    WHEN 3 THEN 10
    WHEN 4 THEN 20
    WHEN 5 THEN 15
    WHEN 6 THEN 25
    WHEN 7 THEN 15
    WHEN 8 THEN 25
    ELSE 0
END;

-- Places visited on the spot now count ten a day, not twenty.
WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY user_id, local_date ORDER BY created_at, id) AS rn
    FROM points_events WHERE kind = 3
)
UPDATE points_events pe SET field_points = 0
FROM ranked r WHERE pe.id = r.id AND r.rn > 10;

-- "First distinct city" is keyed by the shared cities row from now on, so a
-- city reached by a visit and by a walked stop counts once. The earliest row
-- per city takes the new key; any later row for the same city counts 0. This
-- re-keying is not undone by the down migration.
WITH ranked AS (
    SELECT id, city_id,
           ROW_NUMBER() OVER (PARTITION BY user_id, city_id ORDER BY created_at, id) AS rn
    FROM points_events WHERE kind = 4 AND city_id IS NOT NULL
)
UPDATE points_events pe
SET ref_key = CASE WHEN r.rn = 1 THEN 'city:' || r.city_id::text ELSE pe.ref_key END,
    field_points = CASE WHEN r.rn = 1 THEN pe.field_points ELSE 0 END
FROM ranked r WHERE pe.id = r.id;

-- Weekly scores, kept by the award transaction so a board reads one indexed
-- range instead of summing the ledger. Each award writes its city's row and
-- the overall row, whose city_id is the nil uuid.
CREATE TABLE IF NOT EXISTS field_season_scores (
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    city_id       UUID NOT NULL,
    season_id     INTEGER NOT NULL,
    score         BIGINT NOT NULL DEFAULT 0 CHECK (score >= 0),
    places_kept   INTEGER NOT NULL DEFAULT 0 CHECK (places_kept >= 0),
    days_finished INTEGER NOT NULL DEFAULT 0 CHECK (days_finished >= 0),
    PRIMARY KEY (user_id, city_id, season_id)
);
CREATE INDEX IF NOT EXISTS idx_field_season_scores_board
    ON field_season_scores (city_id, season_id, score DESC);

-- Lifetime score per city, for the rank a traveller holds there.
CREATE TABLE IF NOT EXISTS field_city_totals (
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    city_id UUID NOT NULL,
    score   BIGINT NOT NULL DEFAULT 0 CHECK (score >= 0),
    PRIMARY KEY (user_id, city_id)
);

-- A season is closed once its last time zone has finished the week: the
-- runner writes the final positions here and the closure row last, so a
-- half-written snapshot is never read as final.
CREATE TABLE IF NOT EXISTS field_season_snapshots (
    season_id     INTEGER NOT NULL,
    city_id       UUID NOT NULL,
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    score         BIGINT NOT NULL,
    places_kept   INTEGER NOT NULL,
    days_finished INTEGER NOT NULL,
    position      INTEGER NOT NULL,
    PRIMARY KEY (season_id, city_id, user_id)
);
CREATE TABLE IF NOT EXISTS field_season_closures (
    season_id INTEGER PRIMARY KEY,
    closed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Stops a traveller walked or skipped. A side table rather than a column on
-- trip_stops: SaveTrip replaces a trip's days and stops wholesale, and a mark
-- must survive an edit as long as its stop does (stop ids survive saves).
-- No foreign key to trip_stops for the same reason; SaveTrip prunes marks
-- whose stop is gone.
CREATE TABLE IF NOT EXISTS trip_stop_marks (
    stop_id   UUID PRIMARY KEY,
    trip_id   UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    status    SMALLINT NOT NULL CHECK (status IN (2, 3)),
    marked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trip_stop_marks_trip ON trip_stop_marks (trip_id);

-- The neighborhood a place sits in, from a reverse geocode. checked_at is set
-- even when the lookup found none, so a place is asked about once.
ALTER TABLE points_of_interest
    ADD COLUMN IF NOT EXISTS neighborhood TEXT,
    ADD COLUMN IF NOT EXISTS neighborhood_checked_at TIMESTAMPTZ;

-- Whether people who are not friends see the traveller on city boards.
ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS city_board_visible BOOLEAN NOT NULL DEFAULT TRUE;

-- Backfill the aggregates from the rescored ledger.
INSERT INTO field_season_scores (user_id, city_id, season_id, score, places_kept, days_finished)
SELECT user_id, city_id, season_id, SUM(field_points),
       COUNT(*) FILTER (WHERE kind = 10), COUNT(*) FILTER (WHERE kind = 7)
FROM points_events WHERE city_id IS NOT NULL
GROUP BY user_id, city_id, season_id
HAVING SUM(field_points) > 0
ON CONFLICT (user_id, city_id, season_id) DO NOTHING;

INSERT INTO field_season_scores (user_id, city_id, season_id, score, places_kept, days_finished)
SELECT user_id, '00000000-0000-0000-0000-000000000000'::uuid, season_id, SUM(field_points),
       COUNT(*) FILTER (WHERE kind = 10), COUNT(*) FILTER (WHERE kind = 7)
FROM points_events
GROUP BY user_id, season_id
HAVING SUM(field_points) > 0
ON CONFLICT (user_id, city_id, season_id) DO NOTHING;

INSERT INTO field_city_totals (user_id, city_id, score)
SELECT user_id, city_id, SUM(field_points)
FROM points_events WHERE city_id IS NOT NULL
GROUP BY user_id, city_id
HAVING SUM(field_points) > 0
ON CONFLICT (user_id, city_id) DO NOTHING;

-- Lifetime totals become the field score. Streaks are no longer advanced, so
-- the current one is cleared; the longest is kept as history.
UPDATE user_progress up
SET total_points = COALESCE((SELECT SUM(pe.field_points) FROM points_events pe WHERE pe.user_id = up.user_id), 0),
    current_streak = 0,
    updated_at = NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Kinds 9-13 do not exist below this version. The kind-4 re-keying above is
-- not reversed; those rows keep 'city:<cities.id>'.
DELETE FROM points_events WHERE kind > 8;
UPDATE user_progress up
SET total_points = COALESCE((SELECT SUM(pe.points) FROM points_events pe WHERE pe.user_id = up.user_id), 0),
    updated_at = NOW();
ALTER TABLE notification_settings DROP COLUMN IF EXISTS city_board_visible;
ALTER TABLE points_of_interest
    DROP COLUMN IF EXISTS neighborhood_checked_at,
    DROP COLUMN IF EXISTS neighborhood;
DROP TABLE IF EXISTS trip_stop_marks;
DROP TABLE IF EXISTS field_season_closures;
DROP TABLE IF EXISTS field_season_snapshots;
DROP TABLE IF EXISTS field_city_totals;
DROP TABLE IF EXISTS field_season_scores;
ALTER TABLE points_events
    DROP COLUMN IF EXISTS field_points,
    DROP COLUMN IF EXISTS season_id,
    DROP COLUMN IF EXISTS city_id;
ALTER TABLE points_events DROP CONSTRAINT IF EXISTS points_events_kind_check;
ALTER TABLE points_events
    ADD CONSTRAINT points_events_kind_check CHECK (kind BETWEEN 1 AND 8);
-- +goose StatementEnd
