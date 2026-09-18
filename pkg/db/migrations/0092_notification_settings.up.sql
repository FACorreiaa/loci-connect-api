-- +goose Up
-- +goose StatementBegin

-- Notification switches, stored against the account.
--
-- These have lived in browser localStorage keyed by user id, which means they
-- did not follow the account to a second browser or a phone, and nothing
-- server-side could read them — so nothing could ever act on them either.
--
-- This table records the preference. Actually delivering a push or an email is
-- separate work; until that exists the UI must not imply these switches send
-- anything.
CREATE TABLE IF NOT EXISTS notification_settings (
    user_id         UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    recommendations BOOLEAN NOT NULL DEFAULT FALSE,
    trip_reminders  BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_settings;
-- +goose StatementEnd
