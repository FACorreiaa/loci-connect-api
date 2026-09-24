-- +goose Up
-- +goose StatementBegin
-- An APNs token is sent to a topic (the app's bundle id) on one of two hosts.
-- The phone knows both when it registers; the sender needs both to deliver.
ALTER TABLE push_devices
    ADD COLUMN IF NOT EXISTS apns_topic       TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS apns_environment TEXT NOT NULL DEFAULT 'production';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE push_devices
    DROP COLUMN IF EXISTS apns_environment,
    DROP COLUMN IF EXISTS apns_topic;
-- +goose StatementEnd
