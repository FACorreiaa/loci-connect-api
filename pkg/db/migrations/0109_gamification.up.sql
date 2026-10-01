-- +goose Up
-- +goose StatementBegin
-- Points are a ledger, not a counter: one row per thing a traveller did, keyed
-- so the same action can never count twice. ref_key names the action instance
-- ("checkin:2026-10-01", "poi:<id>:2026-10-01", "city:<visited city id>"), and
-- the unique (user_id, kind, ref_key) is what makes every award idempotent: a
-- retried request inserts nothing. local_date is the traveller's own calendar
-- day, which daily caps, streaks and weekly leaderboards are counted in.
CREATE TABLE IF NOT EXISTS points_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       SMALLINT NOT NULL CHECK (kind BETWEEN 1 AND 8),
    ref_key    TEXT NOT NULL,
    points     INTEGER NOT NULL CHECK (points >= 0),
    label      TEXT NOT NULL DEFAULT '',
    local_date DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, kind, ref_key)
);
CREATE INDEX IF NOT EXISTS idx_points_events_user_created
    ON points_events (user_id, created_at DESC);
-- Leaderboards sum a period's points for a handful of users.
CREATE INDEX IF NOT EXISTS idx_points_events_user_date
    ON points_events (user_id, local_date);

-- The running totals the ledger implies, updated in the same transaction as
-- each award so a profile never has to sum the ledger. timezone is the IANA
-- zone the device last reported; it decides what "today" is for this user.
CREATE TABLE IF NOT EXISTS user_progress (
    user_id          UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    total_points     BIGINT NOT NULL DEFAULT 0 CHECK (total_points >= 0),
    current_streak   INTEGER NOT NULL DEFAULT 0 CHECK (current_streak >= 0),
    longest_streak   INTEGER NOT NULL DEFAULT 0 CHECK (longest_streak >= 0),
    last_active_date DATE,
    timezone         TEXT NOT NULL DEFAULT 'UTC',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_badges (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    badge_id   TEXT NOT NULL,
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, badge_id)
);

-- A trip day the traveller walked. A trip is complete when all its days are.
ALTER TABLE trip_days ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

-- The Facebook friends (app-scoped ids) a Limited Login token granted, kept so
-- MatchFacebookFriends can find who else linked Facebook. Replaced wholesale
-- on every LinkFacebook.
CREATE TABLE IF NOT EXISTS user_facebook_friends (
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    facebook_id TEXT NOT NULL,
    PRIMARY KEY (user_id, facebook_id)
);
CREATE INDEX IF NOT EXISTS idx_user_providers_user
    ON user_providers (user_id, provider);

-- At most one progress push of each kind per recipient per local day.
CREATE TABLE IF NOT EXISTS progress_push_log (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    local_date DATE NOT NULL,
    PRIMARY KEY (user_id, kind, local_date)
);

ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS leaderboard_visible BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS streak_reminders BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS progress_updates BOOLEAN NOT NULL DEFAULT TRUE;

-- Backfill what people already did, so the first leaderboard is not empty and
-- nobody starts behind for having used Loci early. No pushes are sent for
-- these rows: they are written here, not through the award path.
-- One NEW_CITY (kind 4, 50 points) per city already on the globe.
INSERT INTO points_events (user_id, kind, ref_key, points, label, local_date, created_at)
SELECT vc.user_id, 4, 'city:' || vc.id::text, 50, 'New city: ' || vc.city_name,
       (vc.first_visit_at AT TIME ZONE 'UTC')::date, vc.first_visit_at
FROM user_visited_cities vc
ON CONFLICT (user_id, kind, ref_key) DO NOTHING;

-- One SCOUT_CLAIM (kind 5, 15 points) per field report others corroborated.
INSERT INTO points_events (user_id, kind, ref_key, points, label, local_date, created_at)
SELECT pc.user_id, 5, 'claim:' || pc.id::text, 15, 'Scout report confirmed',
       (pc.created_at AT TIME ZONE 'UTC')::date, pc.created_at
FROM place_claims pc
WHERE pc.status = 'accepted'
ON CONFLICT (user_id, kind, ref_key) DO NOTHING;

INSERT INTO user_progress (user_id, total_points)
SELECT user_id, SUM(points) FROM points_events GROUP BY user_id
ON CONFLICT (user_id) DO UPDATE SET total_points = EXCLUDED.total_points, updated_at = NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_settings
    DROP COLUMN IF EXISTS progress_updates,
    DROP COLUMN IF EXISTS streak_reminders,
    DROP COLUMN IF EXISTS leaderboard_visible;
DROP TABLE IF EXISTS progress_push_log;
DROP INDEX IF EXISTS idx_user_providers_user;
DROP TABLE IF EXISTS user_facebook_friends;
ALTER TABLE trip_days DROP COLUMN IF EXISTS completed_at;
DROP TABLE IF EXISTS user_badges;
DROP TABLE IF EXISTS user_progress;
DROP TABLE IF EXISTS points_events;
-- +goose StatementEnd
