-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS shares (
    share_code   TEXT PRIMARY KEY,
    content_type INTEGER NOT NULL,            -- mirrors ShareContentType enum
    content_id   TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    image_url    TEXT NOT NULL DEFAULT '',
    created_by   UUID REFERENCES users (id) ON DELETE SET NULL,
    view_count   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shares_created_by ON shares (created_by);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS shares;
-- +goose StatementEnd
