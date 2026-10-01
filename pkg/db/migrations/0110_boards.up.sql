-- +goose Up
-- +goose StatementBegin
-- Community boards: topic boards anyone signed in may open, posts, threaded
-- comments and up/down votes on posts, plus the sanctions an admin uses to
-- mute or ban someone from them.
--
-- Nothing a user writes here is hard-deleted by a moderation action:
-- deleted_at hides it, so a mistaken delete can be undone by hand. Deleting
-- the account still cascades everything away.
CREATE TABLE IF NOT EXISTS boards (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        CITEXT NOT NULL CHECK (slug ~ '^[a-z0-9-]{3,32}$'),
    name        TEXT NOT NULL CHECK (char_length(name) BETWEEN 3 AND 60),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    created_by  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ,
    deleted_by  UUID REFERENCES users (id) ON DELETE SET NULL
);

-- A deleted board frees its slug.
CREATE UNIQUE INDEX IF NOT EXISTS boards_slug_live_unique ON boards (slug) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_boards_created_by ON boards (created_by, created_at DESC);

CREATE TABLE IF NOT EXISTS board_posts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id            UUID NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    author_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title               TEXT NOT NULL CHECK (char_length(title) BETWEEN 3 AND 200),
    url                 TEXT NOT NULL DEFAULT '' CHECK (char_length(url) <= 2048),
    body                TEXT NOT NULL DEFAULT '' CHECK (char_length(body) <= 10000),
    -- The attached Loci item, frozen at post time (see Attachment in the proto).
    attachment_kind     TEXT CHECK (attachment_kind IN ('itinerary', 'poi', 'city')),
    attachment_ref      TEXT,
    attachment_snapshot JSONB,
    score               INT NOT NULL DEFAULT 0,
    upvotes             INT NOT NULL DEFAULT 0 CHECK (upvotes >= 0),
    downvotes           INT NOT NULL DEFAULT 0 CHECK (downvotes >= 0),
    comment_count       INT NOT NULL DEFAULT 0 CHECK (comment_count >= 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ,
    deleted_by          UUID REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT board_posts_attachment_complete CHECK (
        (attachment_kind IS NULL) = (attachment_ref IS NULL)
        AND (attachment_kind IS NULL) = (attachment_snapshot IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_board_posts_new ON board_posts (board_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_board_posts_top ON board_posts (board_id, score DESC, created_at DESC) WHERE deleted_at IS NULL;
-- The all-boards feed and the per-author write limit.
CREATE INDEX IF NOT EXISTS idx_board_posts_all_new ON board_posts (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_board_posts_author ON board_posts (author_id, created_at DESC);

CREATE TABLE IF NOT EXISTS board_comments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id    UUID NOT NULL REFERENCES board_posts (id) ON DELETE CASCADE,
    parent_id  UUID REFERENCES board_comments (id) ON DELETE CASCADE,
    author_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 5000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    deleted_by UUID REFERENCES users (id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_board_comments_post ON board_comments (post_id, created_at);
CREATE INDEX IF NOT EXISTS idx_board_comments_author ON board_comments (author_id, created_at DESC);

-- One vote per person per post; the post's counters move in the same
-- transaction as the vote row.
CREATE TABLE IF NOT EXISTS board_post_votes (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    post_id    UUID NOT NULL REFERENCES board_posts (id) ON DELETE CASCADE,
    value      SMALLINT NOT NULL CHECK (value IN (-1, 1)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);

CREATE INDEX IF NOT EXISTS idx_board_post_votes_user ON board_post_votes (user_id, created_at DESC);

-- A sanction is active while lifted_at is NULL and expires_at is NULL or in
-- the future. A mute stops writes and votes; a ban also hides what the user
-- wrote, at read time, so lifting it brings everything back.
CREATE TABLE IF NOT EXISTS board_sanctions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('mute', 'ban')),
    reason     TEXT NOT NULL DEFAULT '' CHECK (char_length(reason) <= 500),
    expires_at TIMESTAMPTZ,
    created_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lifted_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_board_sanctions_user ON board_sanctions (user_id) WHERE lifted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS board_sanctions;
DROP TABLE IF EXISTS board_post_votes;
DROP TABLE IF EXISTS board_comments;
DROP TABLE IF EXISTS board_posts;
DROP TABLE IF EXISTS boards;
-- +goose StatementEnd
