-- +goose Up
-- +goose StatementBegin

-- A place somebody says exists, held outside points_of_interest until a second
-- person agrees.
--
-- Keeping it out of that table is the whole safety property: the retrieval layer
-- reads points_of_interest, so anything written there can reach a real
-- itinerary. Nothing here is visible to the generator, which means an unverified
-- submission cannot be recommended no matter what any later query forgets to
-- filter.
CREATE TABLE IF NOT EXISTS place_submissions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_submission_id UUID NOT NULL UNIQUE,
    user_id              UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    city_id              UUID NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    category             TEXT,
    latitude             DOUBLE PRECISION,
    longitude            DOUBLE PRECISION,
    address              TEXT,
    website              TEXT,
    status               TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'accepted', 'rejected')),
    poi_id               UUID REFERENCES points_of_interest (id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Identity, copied verbatim from migration 0068's index on
-- points_of_interest, which is what UpsertPOIByIdentity conflicts on. Sharing
-- the expression is what makes "the same place" mean the same thing in both
-- tables. Partial, so a rejected submission does not block a later honest one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_place_submissions_identity
    ON place_submissions (city_id, lower(btrim(name)))
    WHERE status <> 'rejected';

CREATE INDEX IF NOT EXISTS idx_place_submissions_city_status
    ON place_submissions (city_id, status);

-- One row per person per submission, so COUNT(*) is already a count of
-- distinct people and nobody can confirm the same place twice.
CREATE TABLE IF NOT EXISTS place_submission_confirmations (
    submission_id UUID NOT NULL REFERENCES place_submissions (id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (submission_id, user_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS place_submission_confirmations;
DROP TABLE IF EXISTS place_submissions;
-- +goose StatementEnd
