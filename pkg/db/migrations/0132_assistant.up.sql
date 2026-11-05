-- +goose Up
-- +goose StatementBegin
-- The press-and-hold assistant. A conversation is plain text and lasts a day:
-- it is not a chat session, has no city or itinerary, and never appears in the
-- history. What the assistant looked up to answer is not kept.
CREATE TABLE IF NOT EXISTS assistant_conversations
(
    id         UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    messages   JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS assistant_conversations_expires_at_idx
    ON assistant_conversations (expires_at);

-- Saving a place, proposed by the assistant and waiting for the caller. Trip
-- changes use trip_action_proposals; this is what has no trip to hang from.
CREATE TABLE IF NOT EXISTS assistant_action_proposals
(
    id          UUID PRIMARY KEY     DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL,
    poi_id      UUID        NOT NULL,
    poi_name    TEXT        NOT NULL,
    list_id     UUID        NULL,
    list_name   TEXT        NULL,
    summary     TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'pending',
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS assistant_action_proposals_user_idx
    ON assistant_action_proposals (user_id, created_at DESC);

-- What each person spent per day. asks enforces the personal limit; the sum of
-- cost_micro_usd over a day enforces the ceiling on the shared Anthropic budget.
CREATE TABLE IF NOT EXISTS assistant_daily_usage
(
    user_id        UUID   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day            DATE   NOT NULL,
    asks           INT    NOT NULL DEFAULT 0,
    input_tokens   BIGINT NOT NULL DEFAULT 0,
    output_tokens  BIGINT NOT NULL DEFAULT 0,
    cost_micro_usd BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);
CREATE INDEX IF NOT EXISTS assistant_daily_usage_day_idx
    ON assistant_daily_usage (day);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS assistant_daily_usage;
DROP TABLE IF EXISTS assistant_action_proposals;
DROP TABLE IF EXISTS assistant_conversations;
-- +goose StatementEnd
