-- +goose Up
-- +goose StatementBegin
-- A multi-city trip runs one child session per city; the first city's
-- session is the trip's own. Children point at it so the sessions list can
-- show the trip once instead of one entry per city.
ALTER TABLE chat_sessions
    ADD COLUMN IF NOT EXISTS parent_session_id UUID NULL REFERENCES chat_sessions (id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS chat_sessions_parent_idx ON chat_sessions (parent_session_id) WHERE parent_session_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS chat_sessions_parent_idx;
ALTER TABLE chat_sessions DROP COLUMN IF EXISTS parent_session_id;
-- +goose StatementEnd
