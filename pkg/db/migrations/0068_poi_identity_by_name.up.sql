-- +goose Up
-- +goose StatementBegin

-- Narrow POI identity to (city, normalised name).
--
-- 0067 keyed identity on (city, name, coordinates rounded to ~110m). Verified
-- against the live LLM, that is too tight: asked for "museums in Lisbon" twice,
-- the model returned the same museums with coordinates hundreds of metres to
-- kilometres apart, so five of six places still got fresh rows on the second
-- search. Coordinates from a language model are not a stable property of a place.
--
-- Name within a city is what a person means by "the same place", so that is the
-- key. The trade-off is explicit: two genuinely distinct venues sharing a name in
-- one city (chains — "Starbucks", "Café Central") will collapse into one row. For
-- LLM-sourced discovery data that is the lesser problem, and when a real place
-- provider with its own stable ids is wired in, identity should move to
-- (source, source_id) and this index becomes a fallback for AI-sourced rows only.

-- Merge whatever the coordinate-based key let through, keeping the oldest row and
-- repointing everything that references the duplicates.
CREATE TEMPORARY TABLE poi_name_dupes AS
WITH keyed AS (
    SELECT
        id,
        row_number() OVER (
            PARTITION BY city_id, lower(btrim(name))
            ORDER BY created_at ASC, id ASC
        ) AS rn,
        first_value(id) OVER (
            PARTITION BY city_id, lower(btrim(name))
            ORDER BY created_at ASC, id ASC
        ) AS keep_id
    FROM points_of_interest
    WHERE city_id IS NOT NULL
)
SELECT id AS dupe_id, keep_id FROM keyed WHERE rn > 1;

UPDATE reviews r SET poi_id = m.keep_id
FROM poi_name_dupes m WHERE r.poi_id = m.dupe_id;

-- The DELETE-then-UPDATE pairs exist because these tables carry a
-- UNIQUE (user_id, poi_id): merging two rows a user had both saved would collide.
DELETE FROM saved_pois s USING poi_name_dupes m
WHERE s.poi_id = m.dupe_id
  AND EXISTS (SELECT 1 FROM saved_pois k WHERE k.user_id = s.user_id AND k.poi_id = m.keep_id);
UPDATE saved_pois s SET poi_id = m.keep_id
FROM poi_name_dupes m WHERE s.poi_id = m.dupe_id;

DELETE FROM user_favorite_pois f USING poi_name_dupes m
WHERE f.poi_id = m.dupe_id
  AND EXISTS (SELECT 1 FROM user_favorite_pois k WHERE k.user_id = f.user_id AND k.poi_id = m.keep_id);
UPDATE user_favorite_pois f SET poi_id = m.keep_id
FROM poi_name_dupes m WHERE f.poi_id = m.dupe_id;

UPDATE list_items li SET poi_id = m.keep_id
FROM poi_name_dupes m WHERE li.poi_id = m.dupe_id;

UPDATE itinerary_pois ip SET poi_id = m.keep_id
FROM poi_name_dupes m WHERE ip.poi_id = m.dupe_id;

DELETE FROM points_of_interest p USING poi_name_dupes m WHERE p.id = m.dupe_id;

DROP TABLE poi_name_dupes;

-- Swap the index.
DROP INDEX IF EXISTS uniq_poi_city_name_location;

CREATE UNIQUE INDEX IF NOT EXISTS uniq_poi_city_name
    ON points_of_interest (city_id, lower(btrim(name)));

-- +goose StatementEnd
