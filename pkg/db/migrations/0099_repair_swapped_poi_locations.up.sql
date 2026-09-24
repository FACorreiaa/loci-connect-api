-- +goose Up
-- +goose StatementBegin
-- GetOrCreatePOI passed latitude as ST_MakePoint's x, so the POIs it created
-- were stored with latitude and longitude swapped (Vieux Nice at lat 7.3,
-- lon 43.7). The upsert keeps a row's first location, so they never healed.
--
-- A row is repaired only when its city has a center, the stored point is more
-- than 200 km from it, and the swapped point is within 50 km. Rows without a
-- city center, and rows already near their city, are left alone.
UPDATE points_of_interest p
SET location = ST_SetSRID(ST_MakePoint(ST_Y(p.location), ST_X(p.location)), 4326),
    updated_at = NOW()
FROM cities c
WHERE p.city_id = c.id
  AND c.center_location IS NOT NULL
  AND p.location IS NOT NULL
  AND ST_X(p.location) BETWEEN -90 AND 90
  AND ST_DistanceSphere(p.location, c.center_location) > 200000
  AND ST_DistanceSphere(
        ST_SetSRID(ST_MakePoint(ST_Y(p.location), ST_X(p.location)), 4326),
        c.center_location) < 50000;
-- +goose StatementEnd

-- +goose Down
-- Data repair; the swapped values are not worth restoring.
SELECT 1;
