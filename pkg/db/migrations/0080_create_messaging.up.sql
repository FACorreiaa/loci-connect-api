-- +goose Up
-- +goose StatementBegin

-- Chat platforms linked to a Loci account, so somebody can ask for an
-- itinerary from their phone and have it continue in the web app.
--
-- One migration rather than three, because the three tables are meaningless
-- apart: a link with no way to create one, or a delivery watermark for messages
-- nothing can receive, is not a state worth being able to deploy.
CREATE TABLE messaging_links (
    id           uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- Which platform, e.g. 'telegram'.
    platform     text        NOT NULL,

    -- The platform's own id for the conversation — a Telegram chat id. Text
    -- rather than a number: it is somebody else's identifier, and the only
    -- thing Loci does with it is hand it back to them.
    external_id  text        NOT NULL,

    -- How the person is addressed on that platform, for the settings page.
    -- Display only, and refreshed when they message: people rename themselves.
    display_name text        NOT NULL DEFAULT '' CHECK (length(display_name) <= 200),

    linked_at    timestamptz NOT NULL DEFAULT now(),

    -- NULL means linked but never used, which is how the settings page tells a
    -- live connection from one set up and forgotten. Same role as
    -- api_keys.last_used_at.
    last_seen_at timestamptz,

    -- One account per conversation. Without this a chat could be linked to two
    -- accounts and the bot would have to guess whose itinerary to answer with.
    UNIQUE (platform, external_id)
);

-- Listings are always "the platforms this one user has linked".
CREATE INDEX messaging_links_user_idx ON messaging_links (user_id);

-- A short-lived, single-use code that proves a chat belongs to an account.
--
-- The flow it exists for: the web app, where the person is already
-- authenticated, issues a code; they send it to the bot; the bot redeems it.
-- That is what carries the identity across, and it is the only moment an
-- unlinked chat is allowed to reach anything.
CREATE TABLE messaging_link_codes (
    -- The code itself is never stored, only its SHA-256 hash — the same way
    -- api_keys (0057) does it. A database dump therefore cannot be redeemed.
    --
    -- The hash is the primary key because redemption looks a code up by value
    -- and by nothing else.
    code_hash   bytea       PRIMARY KEY,

    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    platform    text        NOT NULL,

    -- Short by design. The code is typed by hand into a chat window, so it is
    -- deliberately weaker than a bearer token; the expiry, the single use, and
    -- a rate limit on redemption are what make that safe rather than length.
    expires_at  timestamptz NOT NULL,

    -- Set when redeemed. A row rather than a delete, so a second attempt with
    -- the same code can be told it was already used instead of looking
    -- identical to a code that never existed.
    redeemed_at timestamptz,

    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX messaging_link_codes_user_idx ON messaging_link_codes (user_id);
CREATE INDEX messaging_link_codes_expiry_idx ON messaging_link_codes (expires_at);

-- How far through a platform's update stream this deployment has read.
--
-- last_update_id exists to recognise a redelivery: the adapter acknowledges an
-- update before it answers it, so the platform resends anything whose
-- acknowledgement was lost, and the watermark is what stops the second copy
-- being answered twice.
--
-- The flaw worth knowing about: a Telegram update id is a counter *per bot*,
-- not a global one. Point the same deployment at a different bot — a rotated
-- token, a test bot swapped for a real one — and the new bot's sequence starts
-- from its own low number. Every update then compares as older than the
-- watermark and is dropped: silent, permanent, logged at info, and impossible
-- to recover from, because the watermark only moves up.
--
-- Recording which bot account produced a watermark makes a bot change a new
-- sequence rather than an old one running backwards. That is the whole reason
-- account_id is in the key.
CREATE TABLE messaging_cursors (
    platform       text        NOT NULL,

    -- The bot's own id on the platform, from getMe.
    account_id     text        NOT NULL,

    last_update_id bigint      NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (platform, account_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE messaging_cursors;
DROP TABLE messaging_link_codes;
DROP TABLE messaging_links;
-- +goose StatementEnd
