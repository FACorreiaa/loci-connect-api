-- +goose Up
-- +goose StatementBegin
-- When the Stripe event behind this row was created. Stripe does not deliver
-- events in order, so without this a late event can overwrite a newer state,
-- such as re-activating a subscription that has since been deleted.
ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS last_stripe_event_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE subscriptions DROP COLUMN IF EXISTS last_stripe_event_at;
-- +goose StatementEnd
