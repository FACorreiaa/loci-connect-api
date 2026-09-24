-- +goose Up
-- +goose StatementBegin
-- One review per user per place. Nothing stopped a second CreateReview for
-- the same POI, so each tap on "Post" added another row and another vote in
-- the POI's average. Keep each user's newest review of a place (ties broken
-- by id) and drop the rest; their helpful votes and replies cascade with them.
-- The update_poi_rating trigger recomputes the POI averages row by row.
DELETE FROM reviews r
USING (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY user_id, poi_id
               ORDER BY created_at DESC, id DESC
           ) AS rn
    FROM reviews
) ranked
WHERE r.id = ranked.id
  AND ranked.rn > 1;

ALTER TABLE reviews
    ADD CONSTRAINT reviews_user_poi_unique UNIQUE (user_id, poi_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE reviews DROP CONSTRAINT IF EXISTS reviews_user_poi_unique;
-- +goose StatementEnd
