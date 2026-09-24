package service

import (
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func planWith(names ...string) *locitypes.AiCityResponse {
	pois := make([]locitypes.POIDetailedInfo, 0, len(names))
	for _, n := range names {
		pois = append(pois, locitypes.POIDetailedInfo{Name: n})
	}
	return &locitypes.AiCityResponse{AIItineraryResponse: locitypes.AIItineraryResponse{PointsOfInterest: pois}}
}

// A follow-up that names another city is a new trip, not an edit to the old
// one. This is the Telegram case: every message from a linked chat continues
// the most recent session, so "itinerary in warsaw" used to land in a Lisbon
// session and be answered with a request to specify the change.
func TestStartsNewTripWhenAnotherCityIsNamed(t *testing.T) {
	if !startsNewTrip("Lisbon", "Warsaw", planWith("Alfama", "Belém Tower")) {
		t.Fatal("a Warsaw request continued the Lisbon session")
	}
}

func TestStartsNewTripIgnoresTheSameCity(t *testing.T) {
	cases := []struct{ session, extracted string }{
		{"Lisbon", "Lisbon"},
		{"Lisbon", "lisbon"},
		{"Lisbon, Portugal", "Lisbon"},
		{"Lisbon", "Lisbon, Portugal"},
		{"Lisbon", ""},
		{"Lisbon", "   "},
	}
	for _, c := range cases {
		if startsNewTrip(c.session, c.extracted, planWith("Alfama")) {
			t.Errorf("session %q + message city %q started a new trip", c.session, c.extracted)
		}
	}
}

// The extractor is a text parser that will happily call a neighbourhood a
// city. A place already in the plan is a reference to the plan, not a move.
func TestStartsNewTripIgnoresPlacesInThePlan(t *testing.T) {
	if startsNewTrip("Lisbon", "Belém", planWith("Alfama", "Belém Tower")) {
		t.Fatal("a place in the itinerary was taken for a new city")
	}
}

// A session with no city cannot be continued at all (the city lookup fails),
// so any named city is better served by a fresh trip.
func TestStartsNewTripWhenTheSessionHasNoCity(t *testing.T) {
	if !startsNewTrip("", "Warsaw", nil) {
		t.Fatal("a session without a city swallowed a Warsaw request")
	}
	if startsNewTrip("", "", nil) {
		t.Fatal("nothing named, nothing to start")
	}
}
