-- +goose Up
-- +goose StatementBegin
ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS external_customer_id TEXT;

CREATE INDEX IF NOT EXISTS idx_subscriptions_external_customer_id
    ON subscriptions (external_customer_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_subscriptions_external_customer_id;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS external_customer_id;
-- +goose StatementEnd
