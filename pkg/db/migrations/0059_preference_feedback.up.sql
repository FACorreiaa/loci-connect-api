-- +goose Up
CREATE TABLE IF NOT EXISTS preference_feedback (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    poi_id      TEXT,
    trip_id     UUID,
    event       TEXT NOT NULL CHECK (event IN (
        'saved', 'skipped', 'reordered', 'visited', 'favorited', 'exported'
    )),
    weight      REAL NOT NULL DEFAULT 1.0,
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_preference_feedback_user_created
    ON preference_feedback (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_preference_feedback_user_event
    ON preference_feedback (user_id, event);

-- +goose Down
DROP TABLE IF EXISTS preference_feedback;
