-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS issued_recommendations (
    user_id             UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    run_id              TEXT NOT NULL,
    item_id             TEXT NOT NULL,
    rank                INTEGER NOT NULL CHECK (rank >= 0),
    algorithm_version   TEXT NOT NULL,
    experiment_variant  TEXT NOT NULL,
    surface             TEXT NOT NULL,
    channel             TEXT NOT NULL,
    issued_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, run_id, item_id, rank)
);

CREATE INDEX IF NOT EXISTS idx_issued_recommendations_run
    ON issued_recommendations (run_id, item_id);

ALTER TABLE recommendation_events
    ADD COLUMN IF NOT EXISTS event_fingerprint BYTEA;

UPDATE recommendation_events
SET event_fingerprint = decode(
    md5(id::text || client_event_id::text) || md5(client_event_id::text || id::text),
    'hex'
)
WHERE event_fingerprint IS NULL;

ALTER TABLE recommendation_events
    ALTER COLUMN event_fingerprint SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_recommendation_events_fingerprint
    ON recommendation_events (event_fingerprint);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_recommendation_events_fingerprint;
ALTER TABLE recommendation_events DROP COLUMN IF EXISTS event_fingerprint;
DROP TABLE IF EXISTS issued_recommendations;
-- +goose StatementEnd
