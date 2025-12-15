-- +goose Up
-- +goose StatementBegin
-- Add phone_verified_at column to users table for phone authentication
ALTER TABLE users
ADD COLUMN IF NOT EXISTS phone_verified_at TIMESTAMPTZ;

-- Create index for phone lookups (only for non-null values)
CREATE INDEX IF NOT EXISTS idx_users_phone ON users (phone)
WHERE
    phone IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_phone;

ALTER TABLE users DROP COLUMN IF EXISTS phone_verified_at;
-- +goose StatementEnd