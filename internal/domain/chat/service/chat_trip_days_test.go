package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func poisWithDays(days ...int) []locitypes.POIDetailedInfo {
	out := make([]locitypes.POIDetailedInfo, len(days))
	for i, d := range days {
		out[i] = locitypes.POIDetailedInfo{ID: uuid.New(), Name: "Place", Day: d}
	}
	return out
}

func daysOf(pois []locitypes.POIDetailedInfo) []int {
	out := make([]int, len(pois))
	for i, p := range pois {
		out[i] = p.Day
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormalizeDays(t *testing.T) {
	cases := []struct {
		name string
		in   []int
		days int
		want []int
	}{
		// A fully and validly numbered list is the model doing its job. Leave
		// it alone — it knows which places are near one another and the
		// redistribution does not.
		{"model numbered it correctly", []int{1, 1, 2, 2, 3, 3}, 3, []int{1, 1, 2, 2, 3, 3}},

		// Uneven but in range is still the model's call.
		{"uneven but valid", []int{1, 2, 2, 2}, 2, []int{1, 2, 2, 2}},

		// Out of range anywhere and the whole list is redistributed: a
		// half-numbered list is worse than an unnumbered one, because the
		// numbered places pin themselves to days the redistribution then has
		// to work around.
		{"invented a day beyond the trip", []int{1, 1, 2, 99}, 2, []int{1, 1, 2, 2}},
		{"numbered from zero", []int{0, 0, 1, 1}, 2, []int{1, 1, 2, 2}},
		{"not numbered at all", []int{0, 0, 0, 0, 0, 0}, 3, []int{1, 1, 2, 2, 3, 3}},
		{"half numbered", []int{1, 1, 0, 0}, 2, []int{1, 1, 2, 2}},

		// More days than places: every place gets its own day and none is
		// pushed past the end.
		{"more days than places", []int{0, 0}, 5, []int{1, 2}},

		// Defensive.
		{"zero days", []int{0, 0}, 0, []int{1, 1}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := daysOf(normalizeDays(poisWithDays(c.in...), c.days))
			if !equal(got, c.want) {
				t.Errorf("normalizeDays(%v, %d) = %v, want %v", c.in, c.days, got, c.want)
			}
		})
	}
}

func TestNormalizeDaysNeverLeavesADayOutOfRange(t *testing.T) {
	for days := 1; days <= 10; days++ {
		for n := 1; n <= 30; n++ {
			in := make([]int, n)
			for i := range in {
				in[i] = i * 7 // nonsense numbering, deliberately
			}
			for _, d := range daysOf(normalizeDays(poisWithDays(in...), days)) {
				if d < 1 || d > days {
					t.Fatalf("%d places over %d days produced day %d", n, days, d)
				}
			}
		}
	}
}

// The point of the change: a four-day trip renders as four days, not as
// ceil(places/4). Twenty-four places used to become six days.
func TestTripSegmentsByParsedDaysNotByCount(t *testing.T) {
	cc := &common.ChatContext{
		UserID:    uuid.New(),
		SessionID: uuid.New(),
		CityName:  "Funchal",
		TripDays:  4,
	}
	pois := make([]locitypes.POIDetailedInfo, 24)
	for i := range pois {
		pois[i] = locitypes.POIDetailedInfo{ID: uuid.New(), Name: "Place"}
	}
	data := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{PointsOfInterest: pois},
	}

	tr := buildTripFromCityResponse(cc, data, "run-1")
	if tr == nil {
		t.Fatal("expected a trip")
	}
	if len(tr.Days) != 4 {
		t.Fatalf("24 places over a 4-day trip produced %d days, want 4", len(tr.Days))
	}
	for i, day := range tr.Days {
		if len(day.Stops) != 6 {
			t.Errorf("day %d has %d stops, want 6", i+1, len(day.Stops))
		}
		if day.DayNumber != int32(i+1) {
			t.Errorf("day at index %d is numbered %d", i, day.DayNumber)
		}
		for j, stop := range day.Stops {
			if stop.OrderIndex != int32(j) {
				t.Errorf("day %d stop %d has order index %d", i+1, j, stop.OrderIndex)
			}
		}
	}
}

// itineraryPOIs is the one definition of "which list is the plan", shared by
// the trip builder and the paged reader. They must not be able to disagree.
func TestItineraryPOIsFallsBackToTheGeneralList(t *testing.T) {
	general := poisWithDays(0, 0)
	data := &locitypes.AiCityResponse{PointsOfInterest: general}
	if got := itineraryPOIs(data); len(got) != 2 {
		t.Errorf("with no itinerary, got %d places from the general list, want 2", len(got))
	}

	data.AIItineraryResponse.PointsOfInterest = poisWithDays(1, 1, 2)
	if got := itineraryPOIs(data); len(got) != 3 {
		t.Errorf("with an itinerary, got %d places, want its own 3", len(got))
	}

	if got := itineraryPOIs(nil); got != nil {
		t.Errorf("nil response produced %v", got)
	}
}
