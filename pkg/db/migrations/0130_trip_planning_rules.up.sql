-- +goose Up
-- +goose StatementBegin
-- Planning together: the owner may let co-planners apply suggestions, not
-- only make them. Off by default, so applying stays with the owner.
ALTER TABLE trips
    ADD COLUMN IF NOT EXISTS co_planners_may_apply BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE trips DROP COLUMN IF EXISTS co_planners_may_apply;
-- +goose StatementEnd
