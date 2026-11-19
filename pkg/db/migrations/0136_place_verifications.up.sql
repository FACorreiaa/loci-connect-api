-- +goose Up
-- +goose StatementBegin
-- What the map says about a place a plan names.
--
-- Places in a plan are written by a language model: the name is usually
-- right, the coordinates drift by kilometres and the hours are a guess. This
-- table holds the result of looking each one up on OpenStreetMap: whether a
-- place by that name was found near the city, where it really is, and its
-- opening hours as the map writes them.
--
-- One row per place, replaced on every re-check. An unmatched row is kept
-- too, so a place that is not on the map is not looked up again on every
-- read. poi_id is TEXT, as in place_facts.
--
-- This is not place_facts: confidence there counts travellers who agree;
-- here it says how closely two names agree.
--
-- Whether a stop is open at its planned time is never stored. It depends on
-- the stop's day and start time, which the traveller edits, so it is worked
-- out when the trip is read.
CREATE TABLE IF NOT EXISTS place_verifications (
    poi_id        TEXT PRIMARY KEY,
    status        TEXT NOT NULL CHECK (status IN ('matched', 'unmatched')),
    source        TEXT NOT NULL DEFAULT 'osm',
    external_id   TEXT NOT NULL DEFAULT '',
    wikidata_id   TEXT NOT NULL DEFAULT '',
    matched_name  TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT '',
    lat           DOUBLE PRECISION,
    lon           DOUBLE PRECISION,
    hours_raw     TEXT NOT NULL DEFAULT '',
    confidence    DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (confidence BETWEEN 0 AND 1),
    checked_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    CONSTRAINT place_verifications_pin_chk CHECK (
        (status = 'unmatched' AND lat IS NULL AND lon IS NULL)
        OR (status = 'matched' AND lat BETWEEN -90 AND 90 AND lon BETWEEN -180 AND 180)
    )
);

CREATE INDEX IF NOT EXISTS idx_place_verifications_expiry
    ON place_verifications (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS place_verifications;
-- +goose StatementEnd
