-- +goose Up
-- +goose StatementBegin
-- One row per brief sent: the morning brief and the weather turn, each at
-- most once per traveller per local day. The runner inserts the row before
-- sending, so two replicas (or two ticks) cannot both send. precip_prob is
-- the morning forecast, which the afternoon check compares against.
CREATE TABLE IF NOT EXISTS trip_brief_log (
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trip_id     UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    local_date  DATE NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('morning', 'weather')),
    precip_prob REAL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, local_date, kind)
);

-- Alerts near saved places that fit the traveller's taste, raised by the
-- device on trip days. Off unless turned on: they need Always location.
ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS nearby_fits BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_settings DROP COLUMN IF EXISTS nearby_fits;
DROP TABLE IF EXISTS trip_brief_log;
-- +goose StatementEnd
