package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// Review Focus #4 / review #7: "now add Porto and Seville" in a Lisbon session
// is a multi-city trip that keeps Lisbon.
func TestNewTripCity_AddingCitiesKeepsTheSessionCity(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Porto"}, {Name: "Seville"}}, Message: "add"})
	l.cache.Set(tripCitiesCacheKey("now add Porto and Seville"), string(raw), time.Hour)
	session := &locitypes.ChatSession{CityName: "Lisbon", SessionContext: locitypes.SessionContext{CityName: "Lisbon"}}

	city, stops, ok := l.newTripCity(context.Background(), session, "now add Porto and Seville", locitypes.IntentModifyItinerary)
	if !ok || city != "Lisbon" {
		t.Fatalf("got %q/%v, want Lisbon/true", city, ok)
	}
	var names []string
	for _, s := range stops {
		names = append(names, s.CityName)
	}
	if strings.Join(names, ",") != "Lisbon,Porto,Seville" {
		t.Fatalf("stops = %v, want Lisbon,Porto,Seville", names)
	}
}

func TestNewTripCity_SessionCityPlusAnotherIsANewTrip(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Lisbon"}, {Name: "Porto"}}, Message: "then"})
	l.cache.Set(tripCitiesCacheKey("Lisbon then Porto"), string(raw), time.Hour)
	session := &locitypes.ChatSession{SessionContext: locitypes.SessionContext{CityName: "Lisbon"}}
	_, stops, ok := l.newTripCity(context.Background(), session, "Lisbon then Porto", locitypes.IntentModifyItinerary)
	if !ok || len(stops) != 2 {
		t.Fatalf("naming the session city and another is a multi-city trip, got %v %v", stops, ok)
	}
}

// Review #7: a question that names two places is a question, not two trips.
func TestNewTripCity_AQuestionNamingTwoPlacesIsNotMultiCity(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	msg := "Is Sintra closer to Lisbon or Cascais?"
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Lisbon"}, {Name: "Cascais"}}, Message: msg})
	l.cache.Set(tripCitiesCacheKey(msg), string(raw), time.Hour)
	session := &locitypes.ChatSession{SessionContext: locitypes.SessionContext{CityName: "Lisbon"}}
	if _, stops, _ := l.newTripCity(context.Background(), session, msg, locitypes.IntentAskQuestion); len(stops) != 0 {
		t.Fatalf("a question must not start a multi-city trip, got %v", stops)
	}
}
