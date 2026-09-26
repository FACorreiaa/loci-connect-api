-- +goose Up
-- +goose StatementBegin
-- The friends layer. Friendship is mutual: one side asks (friend_requests),
-- the other accepts, and only then do the two appear in friendships. A
-- friendship is stored as two rows, one per direction, so "my friends" and
-- "is X my friend" are both a primary-key lookup on user_id.
CREATE TABLE IF NOT EXISTS friendships (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    friend_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, friend_id),
    CONSTRAINT friendships_not_self CHECK (user_id <> friend_id)
);

-- A request stays as a row after it is answered, so the daily send limit and
-- "declined recently" can be read from it. At most one pending request per
-- ordered pair.
CREATE TABLE IF NOT EXISTS friend_requests (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    to_user      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ,
    CONSTRAINT friend_requests_not_self CHECK (from_user <> to_user)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_friend_requests_pending_pair
    ON friend_requests (from_user, to_user) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_friend_requests_to_pending
    ON friend_requests (to_user, created_at DESC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_friend_requests_from_created
    ON friend_requests (from_user, created_at DESC);

-- A block hides each side from the other. Only the blocker can lift it.
CREATE TABLE IF NOT EXISTS user_blocks (
    blocker_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    blocked_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (blocker_id, blocked_id),
    CONSTRAINT user_blocks_not_self CHECK (blocker_id <> blocked_id)
);
CREATE INDEX IF NOT EXISTS idx_user_blocks_blocked ON user_blocks (blocked_id);

-- One live invite code per user; rotating replaces it, so an old link stops
-- working the moment its owner asks.
CREATE TABLE IF NOT EXISTS user_invites (
    user_id    UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    code       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);

-- Contact matching compares the SHA-256 a client sends with the SHA-256 of
-- each user's verified identifier. sha256() is immutable, so the hashes are
-- expression indexes over columns the table already holds: no new copy of
-- anyone's phone or email is kept, and only verified identifiers match.
-- convert_to() is only STABLE, so the hash lives in a function declared
-- IMMUTABLE: the encoding is fixed to UTF-8, so the same text always hashes
-- the same. Queries call the same function, which is what lets them use the
-- indexes.
CREATE OR REPLACE FUNCTION loci_sha256_hex(input TEXT) RETURNS TEXT
    LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE
    AS $$ SELECT encode(sha256(convert_to(input, 'UTF8')), 'hex') $$;
CREATE INDEX IF NOT EXISTS idx_users_phone_sha256
    ON users (loci_sha256_hex(phone))
    WHERE phone IS NOT NULL AND phone_verified_at IS NOT NULL AND is_active;
CREATE INDEX IF NOT EXISTS idx_users_email_sha256
    ON users (loci_sha256_hex(lower(email::text)))
    WHERE email IS NOT NULL AND email_verified_at IS NOT NULL AND is_active;

-- Username search is a prefix match on the case-insensitive username.
CREATE INDEX IF NOT EXISTS idx_users_username_prefix
    ON users (lower(username::text) text_pattern_ops) WHERE username IS NOT NULL;

-- Trip visibility replaces the is_public boolean, which only ever meant "has
-- a link": 1 private, 2 friends, 3 link, 4 public (TripVisibility in
-- trip.proto). A trip already shared by link keeps working as LINK.
-- is_public is kept, written alongside, for anything still reading it.
ALTER TABLE trips
    ADD COLUMN IF NOT EXISTS visibility SMALLINT NOT NULL DEFAULT 1
        CHECK (visibility BETWEEN 1 AND 4),
    ADD COLUMN IF NOT EXISTS share_details BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS copied_from_trip_id UUID REFERENCES trips (id) ON DELETE SET NULL;
UPDATE trips SET visibility = 3 WHERE is_public AND visibility = 1;
-- The friends feed: friends' FRIENDS/PUBLIC trips, newest first.
CREATE INDEX IF NOT EXISTS idx_trips_shared_by_user
    ON trips (user_id, updated_at DESC) WHERE visibility IN (2, 4);

ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS friend_activity BOOLEAN NOT NULL DEFAULT TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_settings DROP COLUMN IF EXISTS friend_activity;
DROP INDEX IF EXISTS idx_trips_shared_by_user;
ALTER TABLE trips
    DROP COLUMN IF EXISTS copied_from_trip_id,
    DROP COLUMN IF EXISTS share_details,
    DROP COLUMN IF EXISTS visibility;
DROP INDEX IF EXISTS idx_users_username_prefix;
DROP INDEX IF EXISTS idx_users_email_sha256;
DROP INDEX IF EXISTS idx_users_phone_sha256;
DROP FUNCTION IF EXISTS loci_sha256_hex(TEXT);
DROP TABLE IF EXISTS user_invites;
DROP TABLE IF EXISTS user_blocks;
DROP TABLE IF EXISTS friend_requests;
DROP TABLE IF EXISTS friendships;
-- +goose StatementEnd
