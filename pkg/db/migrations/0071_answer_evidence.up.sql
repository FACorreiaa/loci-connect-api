-- +goose Up
-- +goose StatementBegin

-- Answer evidence: which stored places were actually put in front of the model,
-- and what it did with them.
--
-- Until now a recommendation could not be traced. The chat path sent the model a
-- city name and a preference paragraph, the model named places from memory, and
-- canonicalizePOIs matched those names back to rows afterwards. The resulting
-- POI id looked like provenance but was a lookup after the fact — it recorded
-- what we found, not what the model was told.
--
-- One row here per (generation, candidate place):
--   * every place offered in the context packet, cited or not
--   * plus any place the model cited that was NOT offered — grounded = FALSE.
--     Those are the fabrications, and they are the reason this table exists.
--
-- Derived state. Safe to truncate: it explains past answers, it does not
-- produce future ones.

CREATE TABLE IF NOT EXISTS answer_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- TEXT to match llm_interactions.session_id and the poi_id convention used
    -- by recommendation_events and user_visited_pois. No FK: llm_interactions is
    -- a TimescaleDB hypertable (migration 0044) and carrying a foreign key into
    -- it would block chunk maintenance.
    llm_interaction_id TEXT NOT NULL CHECK (llm_interaction_id <> ''),

    -- Stable hash over (user, query, city, ordered candidate ids). The same
    -- retrieval recurring across turns is recognisable by this value alone.
    packet_id TEXT NOT NULL CHECK (packet_id <> ''),

    poi_id TEXT NOT NULL CHECK (poi_id <> ''),

    -- Position in the packet offered to the model. -1 for a place the model
    -- cited that was never offered, which by definition has no packet position.
    rank INTEGER NOT NULL DEFAULT -1 CHECK (rank >= -1),

    -- cited:    the model attached this identifier to something it said.
    -- grounded: the identifier was in the packet we assembled.
    --
    -- The four combinations are all meaningful:
    --   cited + grounded      → a real recommendation, traceable to a row
    --   cited + NOT grounded  → a fabricated identifier; never show as verified
    --   NOT cited + grounded  → retrieved and ignored; recall signal
    --   NOT cited + NOT grounded → impossible, rejected by the check below
    cited BOOLEAN NOT NULL DEFAULT FALSE,
    grounded BOOLEAN NOT NULL DEFAULT FALSE,
    CONSTRAINT answer_evidence_cited_or_grounded CHECK (cited OR grounded),

    match_reason TEXT NOT NULL DEFAULT '',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A place appears at most once per generation.
    UNIQUE (llm_interaction_id, poi_id)
);

-- "Show me the evidence behind this answer" — the user-facing read.
CREATE INDEX IF NOT EXISTS idx_answer_evidence_interaction
    ON answer_evidence (llm_interaction_id, rank);

-- "How often does the model cite places we never gave it?" — the metric this
-- whole phase exists to produce. Partial, because the ungrounded rows are the
-- small minority we actually query.
CREATE INDEX IF NOT EXISTS idx_answer_evidence_ungrounded
    ON answer_evidence (created_at DESC)
    WHERE cited AND NOT grounded;

CREATE INDEX IF NOT EXISTS idx_answer_evidence_packet
    ON answer_evidence (packet_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS answer_evidence;
-- +goose StatementEnd
