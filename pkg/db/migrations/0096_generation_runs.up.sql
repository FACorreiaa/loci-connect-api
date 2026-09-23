-- +goose Up
-- +goose StatementBegin
-- One row per generation, so "is my search done?" has an answer that
-- survives a closed tab, a reload, or a pod restart. The id is reserved
-- before the pipeline mints a session id (the concurrency cap needs a row to
-- count), and session_id is attached when the stream's start event arrives.
-- A session can have several turns, each its own run, so session_id is not
-- unique: readers take the newest run for a session.
CREATE TABLE IF NOT EXISTS generation_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id  UUID,
    domain      TEXT NOT NULL DEFAULT '',
    city_name   TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done', 'failed')),
    error_code  TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    notified_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS generation_runs_user_running_idx
    ON generation_runs (user_id, started_at) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS generation_runs_session_idx
    ON generation_runs (session_id, started_at DESC);
-- The partial index above only covers running rows; ON DELETE CASCADE from
-- users needs every row of a user, or deleting an account scans the table.
CREATE INDEX IF NOT EXISTS generation_runs_user_idx ON generation_runs (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS generation_runs;
-- +goose StatementEnd
