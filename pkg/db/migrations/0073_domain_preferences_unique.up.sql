-- +goose Up
-- +goose StatementBegin

-- The four per-domain preference tables (migration 0018) are one row per
-- profile by intent, and nothing has ever enforced it.
--
-- Every writer uses `ON CONFLICT (user_preference_profile_id) DO UPDATE`
-- (profiles/repository_helpers.go). That statement requires a unique constraint
-- on the conflict target. There is none — only a plain btree index — so every
-- one of those writes fails at runtime with SQLSTATE 42P10, "there is no unique
-- or exclusion constraint matching the ON CONFLICT specification".
--
-- So these tables were never readable (nothing selected from them until now) and
-- never writable either. The accommodation, dining, activity and itinerary
-- questions a user answers had nowhere to land.
--
-- Uniqueness also makes the read correct: loadDomainPreferences LEFT JOINs all
-- four onto the profile, and without it a duplicate row would multiply the
-- result set and the "first row wins" read would be arbitrary.

-- Collapse any duplicates before constraining. Newest row wins: these tables are
-- an upsert target, so the most recent write is the intended state. Rows with a
-- NULL profile id are orphans no reader can reach.
DELETE FROM user_accommodation_preferences a
    USING user_accommodation_preferences b
    WHERE a.user_preference_profile_id = b.user_preference_profile_id
      AND a.user_preference_profile_id IS NOT NULL
      AND (a.updated_at, a.id) < (b.updated_at, b.id);

DELETE FROM user_dining_preferences a
    USING user_dining_preferences b
    WHERE a.user_preference_profile_id = b.user_preference_profile_id
      AND a.user_preference_profile_id IS NOT NULL
      AND (a.updated_at, a.id) < (b.updated_at, b.id);

DELETE FROM user_activity_preferences a
    USING user_activity_preferences b
    WHERE a.user_preference_profile_id = b.user_preference_profile_id
      AND a.user_preference_profile_id IS NOT NULL
      AND (a.updated_at, a.id) < (b.updated_at, b.id);

DELETE FROM user_itinerary_preferences a
    USING user_itinerary_preferences b
    WHERE a.user_preference_profile_id = b.user_preference_profile_id
      AND a.user_preference_profile_id IS NOT NULL
      AND (a.updated_at, a.id) < (b.updated_at, b.id);

-- A named UNIQUE constraint rather than a unique index: ON CONFLICT accepts
-- either, but a constraint states the intent in the schema.
ALTER TABLE user_accommodation_preferences
    ADD CONSTRAINT uniq_accommodation_prefs_profile UNIQUE (user_preference_profile_id);
ALTER TABLE user_dining_preferences
    ADD CONSTRAINT uniq_dining_prefs_profile UNIQUE (user_preference_profile_id);
ALTER TABLE user_activity_preferences
    ADD CONSTRAINT uniq_activity_prefs_profile UNIQUE (user_preference_profile_id);
ALTER TABLE user_itinerary_preferences
    ADD CONSTRAINT uniq_itinerary_prefs_profile UNIQUE (user_preference_profile_id);

-- The plain btree indexes from 0018 are now redundant: the unique constraint
-- creates an index that serves the same lookups.
DROP INDEX IF EXISTS idx_user_accommodation_preferences_profile_id;
DROP INDEX IF EXISTS idx_user_dining_preferences_profile_id;
DROP INDEX IF EXISTS idx_user_activity_preferences_profile_id;
DROP INDEX IF EXISTS idx_user_itinerary_preferences_profile_id;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE INDEX IF NOT EXISTS idx_user_accommodation_preferences_profile_id
    ON user_accommodation_preferences (user_preference_profile_id);
CREATE INDEX IF NOT EXISTS idx_user_dining_preferences_profile_id
    ON user_dining_preferences (user_preference_profile_id);
CREATE INDEX IF NOT EXISTS idx_user_activity_preferences_profile_id
    ON user_activity_preferences (user_preference_profile_id);
CREATE INDEX IF NOT EXISTS idx_user_itinerary_preferences_profile_id
    ON user_itinerary_preferences (user_preference_profile_id);

ALTER TABLE user_accommodation_preferences DROP CONSTRAINT IF EXISTS uniq_accommodation_prefs_profile;
ALTER TABLE user_dining_preferences DROP CONSTRAINT IF EXISTS uniq_dining_prefs_profile;
ALTER TABLE user_activity_preferences DROP CONSTRAINT IF EXISTS uniq_activity_prefs_profile;
ALTER TABLE user_itinerary_preferences DROP CONSTRAINT IF EXISTS uniq_itinerary_prefs_profile;

-- +goose StatementEnd
