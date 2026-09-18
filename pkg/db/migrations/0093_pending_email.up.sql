-- +goose Up
-- +goose StatementBegin

-- Where an email change waits until the new address proves it can receive mail.
--
-- ChangeEmail used to write the new address straight onto users.email with
-- nothing sent to it first, so a typo locked the person out of their own
-- account and a stolen session could move the account to an attacker's inbox.
-- The address now sits here until the token mailed to it comes back.
--
-- Not UNIQUE: two people may both be part-way through claiming the same
-- address, and only the one who confirms first gets it. The collision check
-- against users.email happens at confirmation time, when it is decisive.
ALTER TABLE users ADD COLUMN IF NOT EXISTS pending_email TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS pending_email;
-- +goose StatementEnd
