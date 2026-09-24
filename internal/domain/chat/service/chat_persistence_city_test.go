package service

import (
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Places parsed from the model's itinerary carry no city id, and saving them
// with uuid.Nil failed points_of_interest_city_id_fkey on every itinerary
// (prod, 2026-09-24 20:01). They belong to the turn's city.
func TestWithCity_StampsTheTurnsCity(t *testing.T) {
	city, other := uuid.New(), uuid.New()
	in := []locitypes.POIDetailedInfo{{Name: "Ribeira"}, {Name: "Already placed", CityID: other}}
	got := withCity(in, city)
	if got[0].CityID != city {
		t.Fatalf("a place with no city gets the turn's: %v", got[0].CityID)
	}
	if got[1].CityID != other {
		t.Fatal("a place that names its city keeps it")
	}
	if in[0].CityID != uuid.Nil {
		t.Fatal("the caller's slice is not modified")
	}
}
