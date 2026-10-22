-- +goose Up
-- +goose StatementBegin
-- Following replaces mutual friendship. Following is one-way; two people who
-- follow each other are friends, and "Friends" trip visibility is seen only by
-- friends. A row here is an approved follow: following a private account
-- first waits in follow_requests.
CREATE TABLE IF NOT EXISTS follows (
    follower_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    followee_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Lets the older friend RPCs address a follow as a request: a follower the
    -- followee does not follow back is listed to older clients as an incoming
    -- friend request with this id.
    id          UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, followee_id),
    CONSTRAINT follows_not_self CHECK (follower_id <> followee_id)
);
CREATE INDEX IF NOT EXISTS idx_follows_followee ON follows (followee_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_follows_follower_created ON follows (follower_id, created_at DESC);

-- A request to follow a private account. It stays as a row after it is
-- answered, so the daily send limit can be read from it. At most one pending
-- request per ordered pair.
CREATE TABLE IF NOT EXISTS follow_requests (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    to_user      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'approved', 'declined', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ,
    CONSTRAINT follow_requests_not_self CHECK (from_user <> to_user)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_follow_requests_pending_pair
    ON follow_requests (from_user, to_user) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_follow_requests_to_pending
    ON follow_requests (to_user, created_at DESC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_follow_requests_from_created
    ON follow_requests (from_user, created_at DESC);

-- Accounts are public unless their owner makes them private.
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_private BOOLEAN NOT NULL DEFAULT FALSE;

-- Every friendship becomes a follow each way (friendships already holds one
-- row per direction). A pending friend request becomes a follow: nobody is
-- private yet, so nothing needed approving. Blocked pairs are left out.
INSERT INTO follows (follower_id, followee_id, created_at)
SELECT user_id, friend_id, created_at FROM friendships
ON CONFLICT DO NOTHING;

INSERT INTO follows (follower_id, followee_id, created_at)
SELECT r.from_user, r.to_user, r.created_at
FROM friend_requests r
WHERE r.status = 'pending'
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks b
      WHERE (b.blocker_id = r.from_user AND b.blocked_id = r.to_user)
         OR (b.blocker_id = r.to_user AND b.blocked_id = r.from_user))
ON CONFLICT DO NOTHING;

-- friendships is no longer written. It stays readable as a view of mutual
-- follows, so anything still reading it (and an older image after a rollback)
-- sees today's friends. The old table is kept until a later cleanup.
ALTER TABLE friendships RENAME TO friendships_legacy;
CREATE VIEW friendships AS
SELECT a.follower_id AS user_id,
       a.followee_id AS friend_id,
       GREATEST(a.created_at, b.created_at) AS created_at
FROM follows a
JOIN follows b ON b.follower_id = a.followee_id AND b.followee_id = a.follower_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS friendships;
ALTER TABLE friendships_legacy RENAME TO friendships;
INSERT INTO friendships (user_id, friend_id, created_at)
SELECT a.follower_id, a.followee_id, GREATEST(a.created_at, b.created_at)
FROM follows a
JOIN follows b ON b.follower_id = a.followee_id AND b.followee_id = a.follower_id
ON CONFLICT DO NOTHING;
ALTER TABLE users DROP COLUMN IF EXISTS is_private;
DROP TABLE IF EXISTS follow_requests;
DROP TABLE IF EXISTS follows;
-- +goose StatementEnd
