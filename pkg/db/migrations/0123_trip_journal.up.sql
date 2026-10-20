-- +goose Up
-- +goose StatementBegin
-- Trip journal (JournalService). SaveTrip replaces a trip's days and stops
-- wholesale (ids survive), so nothing here references trip_days or trip_stops:
-- a foreign key would cascade on every edit. Rows hang off the trip, and a
-- day or stop that a save removed simply stops showing; its photos fall back
-- to the trip. trip_stop_marks does the same for the same reason.

-- A traveller's note for one day of a trip.
CREATE TABLE IF NOT EXISTS trip_day_diaries (
    day_id     UUID PRIMARY KEY,
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trip_day_diaries_trip ON trip_day_diaries (trip_id);

-- Photos on a trip, optionally pinned to a day or stop. 'pending' from the
-- signed upload until ConfirmPhoto has seen the object; the sweeper removes
-- pending rows older than a day.
CREATE TABLE IF NOT EXISTS trip_photos (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trip_id      UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    day_id       UUID,
    stop_id      UUID,
    object_key   TEXT NOT NULL UNIQUE,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL CHECK (size_bytes > 0),
    caption      TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trip_photos_trip ON trip_photos (trip_id, status);
CREATE INDEX IF NOT EXISTS idx_trip_photos_pending ON trip_photos (created_at) WHERE status = 'pending';

-- Objects whose row is gone (a photo deleted, a trip deleted, a stale upload
-- swept). The trigger records every one, whatever removed the row; the
-- sweeper deletes the objects and then these rows.
CREATE TABLE IF NOT EXISTS trip_photo_tombstones (
    object_key TEXT PRIMARY KEY,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION trip_photo_tombstone() RETURNS trigger AS $$
BEGIN
    INSERT INTO trip_photo_tombstones (object_key) VALUES (OLD.object_key)
    ON CONFLICT (object_key) DO NOTHING;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trip_photos_tombstone ON trip_photos;
CREATE TRIGGER trip_photos_tombstone
    AFTER DELETE ON trip_photos
    FOR EACH ROW EXECUTE FUNCTION trip_photo_tombstone();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trip_photos_tombstone ON trip_photos;
DROP FUNCTION IF EXISTS trip_photo_tombstone();
DROP TABLE IF EXISTS trip_photo_tombstones;
DROP TABLE IF EXISTS trip_photos;
DROP TABLE IF EXISTS trip_day_diaries;
-- +goose StatementEnd
