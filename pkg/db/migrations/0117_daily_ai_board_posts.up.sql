-- +goose Up
-- +goose StatementBegin

ALTER TABLE board_posts
    ADD COLUMN IF NOT EXISTS is_ai_generated BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS publication_date DATE;

CREATE UNIQUE INDEX IF NOT EXISTS board_posts_daily_ai_unique
    ON board_posts (publication_date)
    WHERE is_ai_generated AND deleted_at IS NULL;

-- The editorial account owns the dedicated board. It cannot sign in because
-- it has no password or provider identity; it only supplies a stable author
-- card for public reads.
INSERT INTO users (id, email, username, display_name, role, is_active)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'daily-editorial@lociai.fyi',
    'loci-editorial',
    'Loci Editorial',
    'system',
    TRUE
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO boards (id, slug, name, description, created_by)
VALUES (
    '00000000-0000-0000-0000-000000000010',
    'daily-destinations',
    'Daily destinations',
    'One seasonal city worth considering, published by Loci every day.',
    '00000000-0000-0000-0000-000000000001'
)
ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS board_posts_daily_ai_unique;
ALTER TABLE board_posts
    DROP COLUMN IF EXISTS publication_date,
    DROP COLUMN IF EXISTS is_ai_generated;
DELETE FROM boards WHERE id = '00000000-0000-0000-0000-000000000010';
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000001';
-- +goose StatementEnd
