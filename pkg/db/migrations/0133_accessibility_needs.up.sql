-- +goose Up
-- +goose StatementBegin
-- Self-described accessibility needs on a travel profile.
--
-- GDPR Article 9 special-category (health) data. The application writes
-- accessibility_needs only together with accessibility_consent_at, and clears
-- both together. The CHECK below makes "needs without consent" impossible even
-- if a future code path forgets.
ALTER TABLE user_preference_profiles
    ADD COLUMN IF NOT EXISTS accessibility_needs JSONB,
    ADD COLUMN IF NOT EXISTS accessibility_consent_at TIMESTAMPTZ;

ALTER TABLE user_preference_profiles
    ADD CONSTRAINT user_preference_profiles_accessibility_consent_chk
    CHECK (accessibility_needs IS NULL OR accessibility_consent_at IS NOT NULL);

COMMENT ON COLUMN user_preference_profiles.accessibility_needs IS
    'Special-category data (GDPR Art. 9). Never log, never export to analytics.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_preference_profiles
    DROP CONSTRAINT IF EXISTS user_preference_profiles_accessibility_consent_chk,
    DROP COLUMN IF EXISTS accessibility_consent_at,
    DROP COLUMN IF EXISTS accessibility_needs;
-- +goose StatementEnd
