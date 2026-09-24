-- +goose Up
-- +goose StatementBegin
-- A trip's checklist: packing items and expenses, synced across web and iOS.
-- Versioned independently of trips.version, so ticking an item never conflicts
-- with a stop edit. Item ids are minted by the client (offline edits replay as
-- idempotent upserts), hence the composite key rather than a server default.
-- kind: 1 = packing, 2 = expense (ChecklistItemKind in trip.proto).
CREATE TABLE IF NOT EXISTS trip_checklist_items (
    trip_id      UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    id           UUID NOT NULL,
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         SMALLINT NOT NULL CHECK (kind IN (1, 2)),
    text         TEXT NOT NULL CHECK (char_length(text) BETWEEN 1 AND 300),
    done         BOOLEAN NOT NULL DEFAULT FALSE,
    amount_minor BIGINT NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    currency     TEXT NOT NULL DEFAULT '' CHECK (currency ~ '^([A-Z]{3})?$'),
    position     INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (trip_id, id)
);

-- Packing suggestions the user dismissed for a trip, stored lowercased and
-- trimmed so a suggestion is hidden however its casing drifts.
CREATE TABLE IF NOT EXISTS trip_packing_dismissed (
    trip_id    UUID NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    text_lower TEXT NOT NULL CHECK (char_length(text_lower) BETWEEN 1 AND 300),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (trip_id, text_lower)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trip_packing_dismissed;
DROP TABLE IF EXISTS trip_checklist_items;
-- +goose StatementEnd
