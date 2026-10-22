-- +goose Up
-- +goose StatementBegin
-- Planning a trip together. A member is someone the owner let onto the trip
-- through an invite link; the owner is the trip's user_id and never a row
-- here. Membership is the trip's own list and does not read the follow graph.
CREATE TABLE IF NOT EXISTS trip_members (
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('co_planner', 'viewer')),
    invited_by UUID NULL REFERENCES users (id) ON DELETE SET NULL,
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (trip_id, user_id)
);
-- "Trips I am on", for ListTrips include_member.
CREATE INDEX IF NOT EXISTS idx_trip_members_user ON trip_members (user_id, joined_at DESC);

-- An invite link. It expires, is good for max_uses joins, and the owner can
-- revoke it. One live invite per trip and role is reused rather than minting
-- a new code on every tap of the share button.
CREATE TABLE IF NOT EXISTS trip_invites (
    code       TEXT PRIMARY KEY,
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('co_planner', 'viewer')),
    created_by UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    max_uses   INT NOT NULL CHECK (max_uses > 0),
    uses       INT NOT NULL DEFAULT 0 CHECK (uses >= 0),
    revoked_at TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS idx_trip_invites_trip ON trip_invites (trip_id, role, created_at DESC);

-- One row per accepted invite, so the accept rate limit is a count that holds
-- across replicas, and so "joined through this invite" can be read later.
CREATE TABLE IF NOT EXISTS trip_invite_accepts (
    code        TEXT NOT NULL REFERENCES trip_invites (code) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (code, user_id)
);
CREATE INDEX IF NOT EXISTS idx_trip_invite_accepts_user ON trip_invite_accepts (user_id, accepted_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_invite_accepts;
DROP TABLE IF EXISTS trip_invites;
DROP TABLE IF EXISTS trip_members;
-- +goose StatementEnd
