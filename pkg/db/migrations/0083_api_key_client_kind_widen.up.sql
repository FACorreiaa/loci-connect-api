-- +goose Up
-- +goose StatementBegin

-- Two more clients the settings page writes setup instructions for: Claude
-- Desktop and Cursor.
--
-- The column is constrained to a closed list (see 0078), so adding a client is
-- a migration and a code change together. That is the intended cost — each
-- kind needs its own snippet written and checked — and the two lists must
-- agree: apikey.ClientKinds in the server is the other half of this.
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_client_kind_known;

ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_client_kind_known CHECK (
        client_kind IN ('claude_code', 'claude_desktop', 'cursor', 'codex', 'hermes', 'other')
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Keys minted for the two new kinds become 'other' before the narrower
-- constraint goes back on. Their setup was the generic instructions as far as
-- the old code knew, and refusing the rollback over a presentation label would
-- be worse than losing the label.
UPDATE api_keys SET client_kind = 'other' WHERE client_kind IN ('claude_desktop', 'cursor');

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_client_kind_known;

ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_client_kind_known CHECK (
        client_kind IN ('claude_code', 'codex', 'hermes', 'other')
    );

-- +goose StatementEnd
