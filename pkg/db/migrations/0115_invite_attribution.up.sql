-- +goose Up
-- +goose StatementBegin
-- Who invited whom. Set once, when an account is created from an invite
-- link; never changed afterwards. Only the inviter's id is kept: nothing
-- about how the link travelled (no phone numbers, no contact lists).
ALTER TABLE users ADD COLUMN IF NOT EXISTS invited_by_user_id UUID NULL
    REFERENCES users (id) ON DELETE SET NULL;

-- Invite codes no longer expire: a link sent in a message thread keeps
-- working. NULL expires_at means "never". Rotation still replaces a code.
ALTER TABLE user_invites ALTER COLUMN expires_at DROP NOT NULL;
UPDATE user_invites SET expires_at = NULL;

-- Every account has a code. Same shape as newCode() in the social service:
-- 9 random bytes, base64url, 12 characters. A collision skips that user;
-- GetMyInvite mints one on first use.
INSERT INTO user_invites (user_id, code)
SELECT u.id,
       translate(encode(substring(uuid_send(gen_random_uuid()) FROM 1 FOR 9), 'base64'), '+/', '-_')
FROM users u
WHERE NOT EXISTS (SELECT 1 FROM user_invites i WHERE i.user_id = u.id)
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE user_invites SET expires_at = NOW() + INTERVAL '30 days' WHERE expires_at IS NULL;
ALTER TABLE user_invites ALTER COLUMN expires_at SET NOT NULL;
ALTER TABLE users DROP COLUMN IF EXISTS invited_by_user_id;
-- +goose StatementEnd
