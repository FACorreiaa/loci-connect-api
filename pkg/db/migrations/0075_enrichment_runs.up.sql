-- +goose Up
-- +goose StatementBegin

-- Run records for the background jobs that maintain derived state.
--
-- Two jobs already run against production data and neither leaves any trace:
-- the embedding backfill (poi_embeddings.GenerateEmbeddingsForAllPOIs) and the
-- preference reranker (preference.Reranker.Run), both driven by
-- cmd/preference-rerank. Today the only way to know whether either has ever
-- succeeded is to read the process logs, and the only way to know how stale the
-- embeddings are is to count nulls.
--
-- A job that silently stops is indistinguishable from a job that has nothing to
-- do. This table makes the difference visible: every pass writes a row, whether
-- it succeeded or not.

CREATE TABLE IF NOT EXISTS enrichment_runs (
    run_id TEXT PRIMARY KEY,

    -- Which job. Constrained rather than free text so a typo in a new job's
    -- name cannot quietly create a second, invisible series.
    kind TEXT NOT NULL CHECK (kind IN ('poi_embeddings', 'city_embeddings', 'preference_rerank')),

    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- NULL while the run is in flight. A row with a start and no completion,
    -- older than any plausible runtime, is a job that died mid-pass — which is
    -- exactly the state that used to be invisible.
    completed_at TIMESTAMPTZ,

    items_seen INTEGER NOT NULL DEFAULT 0 CHECK (items_seen >= 0),
    items_updated INTEGER NOT NULL DEFAULT 0 CHECK (items_updated >= 0),
    items_failed INTEGER NOT NULL DEFAULT 0 CHECK (items_failed >= 0),

    -- Non-fatal problems worth surfacing without failing the run.
    warnings JSONB NOT NULL DEFAULT '[]'::jsonb,

    success BOOLEAN NOT NULL DEFAULT FALSE,
    error_summary TEXT,

    CONSTRAINT enrichment_runs_completion CHECK (
        -- A run cannot be successful before it has finished.
        completed_at IS NOT NULL OR NOT success
    )
);

-- "When did this job last succeed?" — the query the health check runs.
CREATE INDEX IF NOT EXISTS idx_enrichment_runs_kind_completed
    ON enrichment_runs (kind, completed_at DESC NULLS LAST);

-- "Is a run currently in flight, or did one die?" Partial, because in-flight
-- rows are a handful at most.
CREATE INDEX IF NOT EXISTS idx_enrichment_runs_in_flight
    ON enrichment_runs (kind, started_at DESC)
    WHERE completed_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS enrichment_runs;
-- +goose StatementEnd
