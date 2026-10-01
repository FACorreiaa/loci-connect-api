-- +goose Up
-- +goose StatementBegin
-- Record the City Pack claims made before 0105 created bundle_claims.
--
-- Trips carry no column naming the pack they came from, so there is no exact
-- link to follow. What a claim does leave is a fingerprint: ClaimBundle copies
-- the pack's title and city_name verbatim, writes no source chat session and
-- no copied-from trip, and copies every stop's day number, order and name.
-- A trip is taken as a pack's claim only when all of that matches exactly —
-- the same (day_number, order_index, name) set on both sides, nothing more
-- and nothing less — and it was written after the pack existed. A trip the
-- user has since edited no longer matches and stays unrecorded, which is
-- exactly the pre-0105 behaviour: their next claim writes one more trip and
-- is recorded.
--
-- When one user has several matching trips (the duplicates that motivated
-- 0105), the oldest becomes the claim. ON CONFLICT leaves any claim recorded
-- since 0105 alone.
INSERT INTO bundle_claims (user_id, bundle_id, trip_id, created_at)
SELECT DISTINCT ON (t.user_id, b.id) t.user_id, b.id, t.id, t.created_at
FROM bundles b
JOIN trips t
  ON t.title = b.title
 AND t.city_name = b.city_name
 AND t.source_session_id IS NULL
 AND t.copied_from_trip_id IS NULL
 AND t.created_at >= b.created_at
WHERE b.status IN ('published', 'retired')
  -- A pack with no stops would match every empty trip of the same title.
  AND EXISTS (
      SELECT 1 FROM bundle_days bd JOIN bundle_stops bs ON bs.bundle_day_id = bd.id
      WHERE bd.bundle_id = b.id
  )
  AND NOT EXISTS (
      (SELECT bd.day_number::int, bs.order_index::int, bs.name
       FROM bundle_days bd JOIN bundle_stops bs ON bs.bundle_day_id = bd.id
       WHERE bd.bundle_id = b.id
       EXCEPT
       SELECT td.day_number::int, ts.order_index::int, ts.name
       FROM trip_days td JOIN trip_stops ts ON ts.day_id = td.id
       WHERE td.trip_id = t.id)
      UNION ALL
      (SELECT td.day_number::int, ts.order_index::int, ts.name
       FROM trip_days td JOIN trip_stops ts ON ts.day_id = td.id
       WHERE td.trip_id = t.id
       EXCEPT
       SELECT bd.day_number::int, bs.order_index::int, bs.name
       FROM bundle_days bd JOIN bundle_stops bs ON bs.bundle_day_id = bd.id
       WHERE bd.bundle_id = b.id)
  )
ORDER BY t.user_id, b.id, t.created_at, t.id
ON CONFLICT (user_id, bundle_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Backfilled rows are indistinguishable from claims recorded at claim time,
-- and deleting a claim only lets the next claim write another trip. Nothing
-- to undo.
SELECT 1;
-- +goose StatementEnd
