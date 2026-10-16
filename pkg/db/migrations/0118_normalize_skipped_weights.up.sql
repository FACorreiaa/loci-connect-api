-- +goose Up
-- +goose StatementBegin
-- preference_feedback.weight is a magnitude; the reranker applies the sign
-- per event (skipped = -1). RecordEvents stored dismissals as -0.5, which the
-- multiplier flipped back to +0.5, so every dismissal pulled the taste vector
-- toward the place that was rejected.
--
-- It also stored RATED events as 'visited' with weight rating/5, so a 1-star
-- rating was a (small) positive signal. RecordEvents is the only writer of
-- 'visited' with a weight below 1, so those rows are re-mapped to the scale
-- preference.RatingSignal now uses:
--   0.2 (1*) -> skipped 1.0   0.4 (2*) -> skipped 0.5
--   0.6 (3*) -> visited 0.2   0.8 (4*) -> visited 0.7
-- The reranker rebuilds vectors and trait evidence from these rows on its
-- next pass, so nothing else needs touching.
UPDATE preference_feedback SET event = 'skipped', weight = 1.0
WHERE event = 'visited' AND weight > 0.1 AND weight < 0.3;

UPDATE preference_feedback SET event = 'skipped', weight = 0.5
WHERE event = 'visited' AND weight > 0.3 AND weight < 0.5;

UPDATE preference_feedback SET weight = 0.2
WHERE event = 'visited' AND weight > 0.5 AND weight < 0.7;

UPDATE preference_feedback SET weight = 0.7
WHERE event = 'visited' AND weight > 0.7 AND weight < 0.9;

UPDATE preference_feedback SET weight = abs(weight)
WHERE event = 'skipped' AND weight < 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Data correction; the buggy values are not worth restoring.
SELECT 1;
-- +goose StatementEnd
