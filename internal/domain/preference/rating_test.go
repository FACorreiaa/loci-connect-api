package preference

import "testing"

func TestRatingSignal(t *testing.T) {
	tests := []struct {
		rating     float64
		wantEvent  string
		wantWeight float32
	}{
		{rating: 5, wantEvent: EventVisited, wantWeight: 1.0},
		{rating: 4, wantEvent: EventVisited, wantWeight: 0.7},
		{rating: 3, wantEvent: EventVisited, wantWeight: 0.2},
		{rating: 2, wantEvent: EventSkipped, wantWeight: 0.5},
		{rating: 1, wantEvent: EventSkipped, wantWeight: 1.0},
		{rating: 0, wantEvent: EventSkipped, wantWeight: 1.0},
		{rating: 4.5, wantEvent: EventVisited, wantWeight: 1.0},
		{rating: 9, wantEvent: EventVisited, wantWeight: 1.0},
	}
	for _, tt := range tests {
		event, weight := RatingSignal(tt.rating)
		if event != tt.wantEvent || weight != tt.wantWeight {
			t.Errorf("RatingSignal(%v) = (%q, %v), want (%q, %v)",
				tt.rating, event, weight, tt.wantEvent, tt.wantWeight)
		}
	}
}

// A low rating must pull the vector away: the stored weight is a magnitude
// and EventMultiplier supplies the sign.
func TestRatingSignal_LowRatingIsNegativeOnceMultiplied(t *testing.T) {
	event, weight := RatingSignal(1)
	if got := EventMultiplier(event) * weight; got >= 0 {
		t.Fatalf("1-star signal = %v, want negative", got)
	}
	event, weight = RatingSignal(5)
	if got := EventMultiplier(event) * weight; got <= 0 {
		t.Fatalf("5-star signal = %v, want positive", got)
	}
}
