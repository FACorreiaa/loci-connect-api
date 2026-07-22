-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS place_claims (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_claim_id  UUID NOT NULL UNIQUE,
    poi_id           TEXT NOT NULL,
    user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    field            TEXT NOT NULL,
    value            TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'accepted', 'contradicted', 'expired')),
    observed_at      TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_place_claims_poi_field
    ON place_claims (poi_id, field, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_place_claims_user
    ON place_claims (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS place_facts (
    poi_id             TEXT NOT NULL,
    field              TEXT NOT NULL,
    value              TEXT NOT NULL,
    confidence         DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    contributor_count  INTEGER NOT NULL DEFAULT 0 CHECK (contributor_count >= 0),
    verified_at        TIMESTAMPTZ NOT NULL,
    expires_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (poi_id, field)
);

CREATE INDEX IF NOT EXISTS idx_place_facts_expiry
    ON place_facts (expires_at);

CREATE TABLE IF NOT EXISTS contributor_profiles (
    user_id           UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    reputation        INTEGER NOT NULL DEFAULT 0 CHECK (reputation BETWEEN 0 AND 100),
    submitted_claims  INTEGER NOT NULL DEFAULT 0 CHECK (submitted_claims >= 0),
    accepted_claims   INTEGER NOT NULL DEFAULT 0 CHECK (accepted_claims >= 0),
    badges            TEXT[] NOT NULL DEFAULT '{}',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS contributor_profiles;
DROP TABLE IF EXISTS place_facts;
DROP TABLE IF EXISTS place_claims;
-- +goose StatementEnd
