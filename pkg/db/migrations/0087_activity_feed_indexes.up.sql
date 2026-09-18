-- +goose NO TRANSACTION
-- +goose Up

-- Indexes for the recents activity feed.
--
-- The feed reads three tables, each newest-first for one user, and merges them.
-- None of the three could answer that from an index before this:
--
--   llm_interactions had (user_id) and (created_at) as separate indexes, so a
--   page meant fetching every row the user had ever produced and sorting it.
--   The composite (user_id, intent, created_at DESC) from migration 0043 only
--   helps when intent is an equality predicate, which is the filtered path, not
--   the default one.
--
--   user_saved_itineraries had (user_id) alone.
--
--   user_favorites had (user_id) and (added_at DESC) separately, which is not
--   the same thing as (user_id, added_at DESC).
--
-- Each index ends in id so it matches the feed's (timestamp, id) ordering
-- exactly. That tiebreaker is not cosmetic: favourites saved in one batch share
-- added_at to the microsecond, and without a second sort key those ties are
-- free to reorder between page fetches, which duplicates some rows and drops
-- others.
--
-- CONCURRENTLY, hence NO TRANSACTION: these run against a live table and a
-- plain CREATE INDEX would hold ACCESS EXCLUSIVE for the duration.

-- Partial on the allowlist the feed uses, so the index holds only rows a person
-- actually asked for. Internal model calls (POI detail lookups) are not in it
-- and cost nothing to skip.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_interactions_activity_feed
    ON llm_interactions (user_id, created_at DESC, id DESC)
    WHERE prompt LIKE 'Unified Chat Stream - Domain: %';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_user_saved_itineraries_user_created
    ON user_saved_itineraries (user_id, created_at DESC, id DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_user_favorites_user_added
    ON user_favorites (user_id, added_at DESC, id DESC);

-- +goose Down

DROP INDEX CONCURRENTLY IF EXISTS idx_user_favorites_user_added;

DROP INDEX CONCURRENTLY IF EXISTS idx_user_saved_itineraries_user_created;

DROP INDEX CONCURRENTLY IF EXISTS idx_llm_interactions_activity_feed;
