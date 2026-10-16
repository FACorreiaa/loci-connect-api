-- +goose Up
-- +goose StatementBegin
-- Story-style recaps: one per finished trip (kind 1), one per calendar year
-- (kind 2). A recap is built once and its cards are stored as a frozen
-- snapshot (payload: loci.recap.Recap wire bytes), because what it is built
-- from keeps moving: taste traits are rewritten nightly, seasons close,
-- friends change. A shared link has to keep saying what it said when posted.
CREATE TABLE IF NOT EXISTS recaps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind            SMALLINT NOT NULL CHECK (kind IN (1, 2)),
    trip_id         UUID REFERENCES trips (id) ON DELETE CASCADE,
    year            SMALLINT,
    -- The traveller's local dates the recap covers.
    period_start    DATE NOT NULL,
    period_end      DATE NOT NULL,
    -- 1 pending, 2 ready, 3 ineligible, 4 failed (RecapStatus in the proto).
    status          SMALLINT NOT NULL DEFAULT 1 CHECK (status BETWEEN 1 AND 4),
    builder_version INTEGER NOT NULL DEFAULT 0,
    payload         BYTEA,
    ineligible_reason TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    build_after     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ready_at        TIMESTAMPTZ,
    notified_at     TIMESTAMPTZ,
    viewed_at       TIMESTAMPTZ,
    share_code      TEXT UNIQUE,
    shared_at       TIMESTAMPTZ,
    view_count      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT recaps_kind_key CHECK (
        (kind = 1 AND trip_id IS NOT NULL) OR (kind = 2 AND year IS NOT NULL)
    ),
    CONSTRAINT recaps_period_order CHECK (period_end >= period_start)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_recaps_trip ON recaps (trip_id) WHERE kind = 1;
CREATE UNIQUE INDEX IF NOT EXISTS uq_recaps_year ON recaps (user_id, year) WHERE kind = 2;
CREATE INDEX IF NOT EXISTS idx_recaps_user_ready ON recaps (user_id, ready_at DESC) WHERE status = 2;
-- The runner's two queues.
CREATE INDEX IF NOT EXISTS idx_recaps_pending ON recaps (build_after) WHERE status = 1;
CREATE INDEX IF NOT EXISTS idx_recaps_unnotified ON recaps (ready_at) WHERE status = 2 AND notified_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS recaps;
-- +goose StatementEnd
