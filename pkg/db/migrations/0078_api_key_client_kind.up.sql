-- +goose Up
-- +goose StatementBegin

-- Which client a key's setup instructions were written for.
--
-- Presentation only. Nothing about authentication varies by kind: the MCP
-- middleware authenticates a key and injects its owner, and a key minted for
-- Claude Code works just as well pasted into Codex. It exists because the
-- settings page lists keys by name and date, and "which one is on the laptop"
-- is easier to answer when the row also says what it was minted for — which is
-- the question somebody is actually asking when they revoke one.
--
-- Constrained rather than free text so the page never has to render a kind it
-- has no name or icon for. Adding a client is then a migration and a code
-- change together, which is the honest cost: a new kind needs its own setup
-- snippet written and verified anyway.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS client_kind TEXT NOT NULL DEFAULT 'other';

ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_client_kind_known CHECK (
        client_kind IN ('claude_code', 'codex', 'hermes', 'other')
    );

-- Keys minted before this column existed keep the default. They were created
-- through the generic instructions, which is exactly what 'other' means, so
-- there is nothing to backfill.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_client_kind_known;
ALTER TABLE api_keys DROP COLUMN IF EXISTS client_kind;
-- +goose StatementEnd
