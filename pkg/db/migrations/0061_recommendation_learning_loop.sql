-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS personalization_settings (
    user_id                 UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    personalization_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    contribute_aggregate    BOOLEAN NOT NULL DEFAULT FALSE,
    disclosure_seen         BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS recommendation_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_event_id     UUID NOT NULL UNIQUE,
    user_id             UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    event_type          TEXT NOT NULL,
    run_id              TEXT NOT NULL,
    item_id             TEXT NOT NULL,
    poi_id              TEXT,
    trip_id             UUID,
    rank                INTEGER NOT NULL CHECK (rank >= 0),
    algorithm_version   TEXT NOT NULL,
    experiment_variant  TEXT NOT NULL,
    surface             TEXT NOT NULL,
    channel             TEXT NOT NULL,
    rating              SMALLINT CHECK (rating BETWEEN 1 AND 5),
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    contribute_aggregate BOOLEAN NOT NULL DEFAULT FALSE,
    occurred_at         TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_recommendation_events_user_occurred
    ON recommendation_events (user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_recommendation_events_run
    ON recommendation_events (run_id, rank);
CREATE INDEX IF NOT EXISTS idx_recommendation_events_algorithm_outcome
    ON recommendation_events (algorithm_version, experiment_variant, event_type, occurred_at DESC);

CREATE TABLE IF NOT EXISTS user_taste_traits (
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trait_key       TEXT NOT NULL,
    label           TEXT NOT NULL,
    score           DOUBLE PRECISION NOT NULL CHECK (score BETWEEN -1 AND 1),
    confidence      DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    evidence_count  INTEGER NOT NULL DEFAULT 0 CHECK (evidence_count >= 0),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, trait_key)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_taste_traits;
DROP TABLE IF EXISTS recommendation_events;
DROP TABLE IF EXISTS personalization_settings;
-- +goose StatementEnd
