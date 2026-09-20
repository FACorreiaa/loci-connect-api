-- +goose Up
-- +goose StatementBegin

-- OAuth calendar connections (Google Calendar, Calendly). Apple Calendar is
-- device-local EventKit and never stored here.
--
-- Distinct from integration_connections (0079), which holds MCP gateway
-- addresses a person typed. These rows hold sealed refresh tokens for
-- first-party calendar APIs.
CREATE TABLE calendar_connections (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- 'google' or 'calendly'. Validated in the service, not CHECK-constrained,
    -- so a new provider is one code change rather than a migration.
    provider      text        NOT NULL,

    -- Email or scheduling URL shown in Settings. Never a token.
    account_label text        NOT NULL DEFAULT '' CHECK (length(account_label) <= 320),

    -- AES-256-GCM refresh token, sealed by pkg/secret with the user id as
    -- additional data — same construction as user_ai_credentials (0077).
    refresh_token bytea       NOT NULL CHECK (length(refresh_token) BETWEEN 30 AND 8192),

    connected_at  timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    UNIQUE (user_id, provider)
);

CREATE INDEX calendar_connections_user_idx ON calendar_connections (user_id);

-- Personal ICS subscribe URL (Apple Calendar on the web). Independent of
-- Google/Calendly OAuth: it is the user's dated Loci trips.
CREATE TABLE calendar_feeds (
    user_id    uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    token      text        NOT NULL UNIQUE CHECK (length(token) BETWEEN 16 AND 64),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE calendar_feeds;
DROP TABLE calendar_connections;
-- +goose StatementEnd
