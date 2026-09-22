-- +goose Up
-- +goose StatementBegin
-- Where to deliver a push. A browser's web-push subscription (or, later, a
-- phone's APNs token) belongs to one account at a time; re-registering an
-- endpoint moves it to whoever is signed in now.
CREATE TABLE IF NOT EXISTS push_devices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    platform     TEXT NOT NULL CHECK (platform IN ('web_push', 'apns')),
    endpoint     TEXT NOT NULL UNIQUE,
    p256dh       TEXT NOT NULL DEFAULT '',
    auth         TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS push_devices_user_idx ON push_devices (user_id);

-- The first switch that actually sends something. On by default: the
-- browser's own permission prompt, asked in context, is the consent.
ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS search_finished BOOLEAN NOT NULL DEFAULT TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_settings DROP COLUMN IF EXISTS search_finished;
DROP TABLE IF EXISTS push_devices;
-- +goose StatementEnd
