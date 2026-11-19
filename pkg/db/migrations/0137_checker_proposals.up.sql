-- +goose Up
-- +goose StatementBegin
-- The stop checker offers changes too: a stop whose place is shut on its day,
-- moved to a day it is open. user_id is the trip's owner, who applies it.
ALTER TABLE trip_action_proposals
    DROP CONSTRAINT IF EXISTS trip_action_proposals_source_check;
ALTER TABLE trip_action_proposals
    ADD CONSTRAINT trip_action_proposals_source_check
        CHECK (source IN ('chat', 'editor', 'checker'));

-- Nobody asks for a checker offer; a read of the trip sets it off, and two
-- reads can land together. At most one open offer stands per stop.
CREATE UNIQUE INDEX IF NOT EXISTS idx_trip_action_proposals_checker_stop
    ON trip_action_proposals (trip_id, (action ->> 'stop_id'))
    WHERE source = 'checker' AND status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_trip_action_proposals_checker_stop;
-- The older constraint has no room for these rows. They are offers worked
-- out from the map and can be worked out again.
DELETE FROM trip_action_proposals WHERE source = 'checker';
ALTER TABLE trip_action_proposals
    DROP CONSTRAINT IF EXISTS trip_action_proposals_source_check;
ALTER TABLE trip_action_proposals
    ADD CONSTRAINT trip_action_proposals_source_check
        CHECK (source IN ('chat', 'editor'));
-- +goose StatementEnd
