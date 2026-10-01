-- +goose Up
-- +goose StatementBegin
-- What the chat agent proposed to change on a trip, waiting for the
-- traveller to confirm. Stored server-side so a proposal is applied at most
-- once, cannot be altered between proposing and applying, and fits in a
-- Telegram button (callback_data is capped at 64 bytes; an id fits, an
-- action does not). Expiry is read at apply time rather than swept.
CREATE TABLE IF NOT EXISTS trip_action_proposals (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    session_id UUID NULL,
    action     JSONB NOT NULL,
    options    JSONB NOT NULL DEFAULT '[]',
    summary    TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'applied', 'dismissed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trip_action_proposals_trip
    ON trip_action_proposals (trip_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_action_proposals;
-- +goose StatementEnd
