package preference

import "math"

// RatingSignal maps a 1–5 rating to the feedback event and weight that
// teaches the preference vector. The weight is a magnitude: low ratings are
// recorded as skipped so EventMultiplier pulls the vector away from the place,
// and a middling rating barely moves it. Ratings round half-up and are
// clamped to 1..5.
func RatingSignal(rating float64) (event string, weight float32) {
	r := math.Floor(rating + 0.5)
	r = math.Max(1, math.Min(5, r))
	switch r {
	case 5:
		return EventVisited, 1.0
	case 4:
		return EventVisited, 0.7
	case 3:
		return EventVisited, 0.2
	case 2:
		return EventSkipped, 0.5
	default:
		return EventSkipped, 1.0
	}
}
