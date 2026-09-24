-- +goose Up
-- +goose StatementBegin
-- Standing tasks: something a user asked Loci to keep an eye on. A due watch
-- is run by the server and its answer appended to session_id's thread as a
-- proactive assistant message (origin "proactive", source_label
-- "Standing task").
--
-- Chat messages themselves need no schema change: they live in
-- chat_sessions.conversation_history (JSONB), where origin and source_label
-- are optional keys. A message without them is a reply.
CREATE TABLE IF NOT EXISTS chat_watches (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id       UUID NOT NULL REFERENCES chat_sessions (id) ON DELETE CASCADE,
    title            TEXT NOT NULL,
    schedule_human   TEXT NOT NULL,
    spec             TEXT NOT NULL,
    interval_minutes INTEGER NOT NULL CHECK (interval_minutes BETWEEN 60 AND 43200),
    next_run_at      TIMESTAMPTZ NOT NULL,
    last_run_at      TIMESTAMPTZ,
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- The runner's only query: enabled watches whose time has come.
CREATE INDEX IF NOT EXISTS chat_watches_due_idx ON chat_watches (next_run_at) WHERE enabled;
CREATE INDEX IF NOT EXISTS chat_watches_user_idx ON chat_watches (user_id, session_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS chat_watches;
-- +goose StatementEnd
