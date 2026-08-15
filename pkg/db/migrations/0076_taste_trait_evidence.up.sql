-- +goose Up
-- +goose StatementBegin

-- What taught Loci each thing it believes about a person.
--
-- The learning loop already has both ends: preference_feedback records what a
-- user did, and user_taste_traits holds what was concluded (a score, a
-- confidence, an evidence_count). What is missing is the join between them. A trait says "likes natural wine, confidence 0.8, from
-- 4 signals" and there is no way to ask *which* four.
--
-- Without that link the taste profile is an assertion the user cannot check, and
-- "forget this about me" can only mean wiping everything. With it, a trait can be
-- explained, disputed, and removed one piece at a time — and the vector and
-- traits can be rebuilt from whatever evidence survives, exactly the way a
-- disposable index rebuilds from its sources.

CREATE TABLE IF NOT EXISTS taste_trait_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trait_key TEXT NOT NULL CHECK (trait_key <> ''),

    -- The action that contributed. preference_feedback, not
    -- recommendation_events: the reranker derives traits by joining
    -- preference_feedback to points_of_interest, so that is the table an
    -- explanation has to point at to be true.
    --
    -- ON DELETE CASCADE: deleting the underlying signal must not leave an
    -- explanation pointing at nothing.
    feedback_id UUID NOT NULL
        REFERENCES preference_feedback (id) ON DELETE CASCADE,

    -- How much this signal pushed the trait, signed. Negative weights are real:
    -- skipping a recommendation is evidence against a preference, and the
    -- reranker already treats it that way (EventMultiplier skipped = -1.0).
    weight DOUBLE PRECISION NOT NULL,

    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One event contributes to a given trait at most once. A recompute that ran
    -- twice must not double the apparent evidence behind a belief.
    UNIQUE (user_id, trait_key, feedback_id)
);

-- "Why do you think this about me?" — the user-facing read, newest first.
CREATE INDEX IF NOT EXISTS idx_taste_trait_evidence_user_trait
    ON taste_trait_evidence (user_id, trait_key, occurred_at DESC);

-- "Forget this action" — find every belief it fed.
CREATE INDEX IF NOT EXISTS idx_taste_trait_evidence_feedback
    ON taste_trait_evidence (feedback_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS taste_trait_evidence;
-- +goose StatementEnd
