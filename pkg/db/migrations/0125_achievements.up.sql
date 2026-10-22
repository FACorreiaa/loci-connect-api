-- +goose Up
-- +goose StatementBegin
-- Achievements replace user_badges and the contributor badges. A track
-- (cities, stops, reviews...) has four tiers, and each one earned is a row; a
-- one-off moment or a secret is a single row at tier 0. Rows are only ever
-- added: an achievement earned stays earned even if the count behind it later
-- drops (a deleted review, an unsaved place).
CREATE TABLE IF NOT EXISTS user_achievements (
    user_id        UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    achievement_id TEXT NOT NULL CHECK (achievement_id <> ''),
    tier           SMALLINT NOT NULL DEFAULT 0 CHECK (tier BETWEEN 0 AND 4),
    earned_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- backfill: earned for what the user did before achievements existed.
    -- Those never push and never reach anyone's feed, and the app reveals them
    -- once instead of one at a time.
    source         TEXT NOT NULL DEFAULT 'live' CHECK (source IN ('live', 'backfill')),
    seen_at        TIMESTAMPTZ,
    PRIMARY KEY (user_id, achievement_id, tier)
);
CREATE INDEX IF NOT EXISTS idx_user_achievements_unseen
    ON user_achievements (user_id) WHERE seen_at IS NULL;

-- How many people hold each achievement, for its rarity. Refreshed by the
-- field runner; the row with achievement_id '' and tier 0 is the base the
-- percentages are taken over (people who explored in the last year).
CREATE TABLE IF NOT EXISTS achievement_stats (
    achievement_id TEXT NOT NULL,
    tier           SMALLINT NOT NULL DEFAULT 0,
    holders        INTEGER NOT NULL DEFAULT 0 CHECK (holders >= 0),
    refreshed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (achievement_id, tier)
);

-- Set once a user's past has been evaluated. Until then their first
-- evaluation is a backfill, whatever triggers it. Everyone here now starts
-- NULL; anyone who signs up later has no past, so their column defaults to
-- their signup and their first achievement is live.
ALTER TABLE users ADD COLUMN IF NOT EXISTS achievements_backfilled_at TIMESTAMPTZ;
ALTER TABLE users ALTER COLUMN achievements_backfilled_at SET DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_users_achievements_backfill
    ON users (id) WHERE achievements_backfilled_at IS NULL;

-- The weekly streak: consecutive ISO weeks (Monday to Sunday, local) with any
-- exploration. last_streak_week is the Monday of the latest week counted, and
-- week_streak is the run ending there; whether that run is still alive is
-- decided when it is read.
ALTER TABLE user_progress
    ADD COLUMN IF NOT EXISTS week_streak         INTEGER NOT NULL DEFAULT 0 CHECK (week_streak >= 0),
    ADD COLUMN IF NOT EXISTS longest_week_streak INTEGER NOT NULL DEFAULT 0 CHECK (longest_week_streak >= 0),
    ADD COLUMN IF NOT EXISTS last_streak_week    DATE;

-- Weekly streaks from the ledger so far. Exploration is a visit, a new city,
-- a finished day or trip, a walked stop or a new neighborhood (kinds 3, 4, 7,
-- 8, 11, 12). Runs of consecutive weeks share wk - 7 * row_number.
WITH weeks AS (
    SELECT DISTINCT user_id, date_trunc('week', local_date)::date AS wk
    FROM points_events
    WHERE kind IN (3, 4, 7, 8, 11, 12)
), runs AS (
    SELECT user_id, wk,
           wk - (7 * ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY wk))::int AS grp
    FROM weeks
), islands AS (
    SELECT user_id, COUNT(*)::int AS len, MAX(wk) AS last_wk
    FROM runs GROUP BY user_id, grp
), latest AS (
    SELECT DISTINCT ON (user_id) user_id, len, last_wk,
           MAX(len) OVER (PARTITION BY user_id) AS longest
    FROM islands
    ORDER BY user_id, last_wk DESC
)
INSERT INTO user_progress (user_id, week_streak, longest_week_streak, last_streak_week)
SELECT user_id, len, longest, last_wk FROM latest
ON CONFLICT (user_id) DO UPDATE SET
    week_streak         = EXCLUDED.week_streak,
    longest_week_streak = EXCLUDED.longest_week_streak,
    last_streak_week    = EXCLUDED.last_streak_week;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_progress
    DROP COLUMN IF EXISTS last_streak_week,
    DROP COLUMN IF EXISTS longest_week_streak,
    DROP COLUMN IF EXISTS week_streak;
ALTER TABLE users DROP COLUMN IF EXISTS achievements_backfilled_at;
DROP TABLE IF EXISTS achievement_stats;
DROP TABLE IF EXISTS user_achievements;
-- +goose StatementEnd
