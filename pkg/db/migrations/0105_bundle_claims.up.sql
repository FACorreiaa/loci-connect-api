-- +goose Up
-- +goose StatementBegin
-- One claimed trip per user per pack. ClaimBundle used to insert a new trip on
-- every call, so a double tap or a retry after a dropped response left the
-- user with copies of the same pack. The claim row points at the trip it
-- produced; ClaimBundle returns that trip instead of writing another.
--
-- trip_id cascades: a user who deletes the claimed trip can claim the pack
-- again and get a fresh copy. Claims made before this migration were never
-- recorded, so those users' next claim writes one more trip and is recorded.
CREATE TABLE IF NOT EXISTS bundle_claims (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    bundle_id  UUID NOT NULL REFERENCES bundles (id) ON DELETE CASCADE,
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, bundle_id)
);

CREATE INDEX IF NOT EXISTS idx_bundle_claims_trip ON bundle_claims (trip_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS bundle_claims;
-- +goose StatementEnd
