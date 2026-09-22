-- +goose Up
-- +goose StatementBegin
-- One row per generation, so "is my search done?" has an answer that
-- survives a closed tab, a reload, or a pod restart. The id is reserved
-- before the pipeline mints a session id (the concurrency cap needs a row to
-- count), and session_id is attached when the stream's start event arrives.
CREATE TABLE IF NOT EXISTS generation_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id  UUID UNIQUE,
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS generation_runs;
-- +goose StatementEnd
