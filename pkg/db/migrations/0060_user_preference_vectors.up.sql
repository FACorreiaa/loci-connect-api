-- +goose Up
-- +goose StatementBegin
-- Per-user preference embedding learned from preference_feedback (save/favorite/…).
-- Separate from user_interests.preference_embedding so one vector represents the
-- whole account rather than a single interest junction row.
CREATE TABLE IF NOT EXISTS user_preference_vectors (
    user_id          UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    embedding        VECTOR(768) NOT NULL,
    feedback_count   INTEGER NOT NULL DEFAULT 0,
    last_feedback_at TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_preference_vectors_hnsw
    ON user_preference_vectors
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

COMMENT ON TABLE user_preference_vectors IS
    'Aggregated user taste vector from preference_feedback + POI embeddings';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_preference_vectors;
-- +goose StatementEnd
