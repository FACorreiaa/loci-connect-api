-- +goose Up
-- +goose StatementBegin

-- TOTP multi-factor auth.
--
-- Two tables rather than columns on `users`: the secret and the recovery codes
-- have a different sensitivity and a different lifecycle from profile data, and
-- keeping them separate means an accidental `SELECT *` on users cannot leak them.

CREATE TABLE IF NOT EXISTS user_mfa (
    user_id UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,

    -- The TOTP shared secret, AES-GCM encrypted with MFA_SECRET_KEY. Never stored
    -- in plaintext: a database dump must not be enough to mint valid codes.
    secret_encrypted BYTEA NOT NULL,

    -- Enrolment is two-step. A row exists from the moment a secret is issued, but
    -- MFA is only enforced once the user has proved they can generate a code —
    -- otherwise a failed enrolment would lock them out of their own account.
    confirmed_at TIMESTAMPTZ,

    -- The last TOTP time-step accepted for this user. A valid code is accepted
    -- exactly once: within its 30-second window it would otherwise be replayable
    -- by anyone who observed it.
    last_used_step BIGINT,

    -- Verification attempt throttling. A 6-digit code is trivially brute-forced
    -- without it.
    failed_attempts INT NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Single-use recovery codes, for the case that actually happens: a lost phone.
-- Hashed like passwords — the plaintext is shown exactly once, at generation.
CREATE TABLE IF NOT EXISTS user_mfa_recovery_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Verifying a recovery code means checking a candidate against the user's unused
-- hashes, so that is the access pattern to index.
CREATE INDEX IF NOT EXISTS idx_mfa_recovery_unused
    ON user_mfa_recovery_codes (user_id)
    WHERE used_at IS NULL;

-- +goose StatementEnd
