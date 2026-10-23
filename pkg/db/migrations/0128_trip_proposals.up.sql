-- +goose Up
-- +goose StatementBegin
-- Planning together: a trip's members suggest changes too. user_id stays the
-- proposer (the owner, for the planner's own chat cards); the owner applies.
ALTER TABLE trip_action_proposals
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'chat'
        CHECK (source IN ('chat', 'editor')),
    -- The trip version the proposer was looking at. Informational: applying
    -- builds on the trip as it is, and a stop that is gone refuses the edit.
    ADD COLUMN IF NOT EXISTS base_version BIGINT NULL,
    ADD COLUMN IF NOT EXISTS applied_by UUID NULL REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ NULL;

-- 👍 / 👎 on a proposal, one per person.
CREATE TABLE IF NOT EXISTS trip_proposal_reactions (
    proposal_id UUID NOT NULL REFERENCES trip_action_proposals (id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    value       SMALLINT NOT NULL CHECK (value IN (-1, 1)),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (proposal_id, user_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_proposal_reactions;
ALTER TABLE trip_action_proposals
    DROP COLUMN IF EXISTS resolved_at,
    DROP COLUMN IF EXISTS applied_by,
    DROP COLUMN IF EXISTS base_version,
    DROP COLUMN IF EXISTS source;
-- +goose StatementEnd
