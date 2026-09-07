-- +goose Up
-- +goose StatementBegin

-- External MCP servers Loci may call on the user's behalf while planning:
-- their own Hermes instance, a calendar.
--
-- This is the direction Loci does not usually run in. Everywhere else it is the
-- MCP server and somebody's agent calls it; here it is the client, dialling an
-- address a person typed into a form. Every constraint below follows from that.
CREATE TABLE integration_connections (
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- Which integration, e.g. 'hermes' or 'calendar'. Not CHECK-constrained
    -- against a literal list: the valid set lives in the service, and
    -- repeating it here would make two places to edit and one to forget.
    provider     text        NOT NULL,

    -- The MCP endpoint. Validated by providers.ParseGatewayURL before it is
    -- stored and again before it is dialled — the second time because a name
    -- that resolved to a public host when it was saved can be rebound onto
    -- loopback or cloud metadata afterwards.
    endpoint     text        NOT NULL CHECK (length(endpoint) BETWEEN 1 AND 500),

    -- AES-256-GCM, sealed by pkg/secret with the user id as additional data,
    -- exactly as user_ai_credentials (0077) does it.
    --
    -- NULL rather than empty for "this server needs no token", so the two
    -- cases stay distinguishable: a server that wants no credential, and one
    -- whose credential we failed to store. The length floor is the minimum
    -- size of a sealed value and catches a truncated blob.
    access_token bytea       CHECK (access_token IS NULL OR length(access_token) BETWEEN 30 AND 8192),

    -- NULL means registered but never reached, which is how the settings page
    -- tells a live connection from one set up and forgotten. Same role as
    -- api_keys.last_used_at.
    last_seen_at timestamptz,

    -- Why the last call failed. Bounded because it holds an error message from
    -- a server that is not ours, whose length is not ours to trust.
    last_error   text        NOT NULL DEFAULT '' CHECK (length(last_error) <= 500),

    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),

    -- One connection per integration per account. Reconnecting replaces rather
    -- than accumulates: two endpoints for the same provider would need a third
    -- concept — which of them is live — for no benefit anyone asked for.
    PRIMARY KEY (user_id, provider)
);

-- Listings are always "the servers this one user has connected".
CREATE INDEX integration_connections_user_idx ON integration_connections (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE integration_connections;
-- +goose StatementEnd
