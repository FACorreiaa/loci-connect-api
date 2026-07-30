package compare

import (
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/multicity"
)

func TestHaversinePortoEvora(t *testing.T) {
	// Porto → Évora straight-line ~290 km (user fixture: ~300 km from Porto).
	const (
		portoLat = 41.1579
		portoLon = -8.6291
		evoraLat = 38.5714
		evoraLon = -7.9135
	)
	km := HaversineKm(portoLat, portoLon, evoraLat, evoraLon)
	if km < 250 || km > 350 {
		t.Fatalf("Porto→Évora distance %.1f km outside expected band 250–350", km)
	}
}

func TestHaversineEvoraBeja(t *testing.T) {
	const (
		evoraLat = 38.5714
		evoraLon = -7.9135
		bejaLat  = 38.0153
		bejaLon  = -7.8624
	)
	km := HaversineKm(evoraLat, evoraLon, bejaLat, bejaLon)
	if km < 50 || km > 100 {
		t.Fatalf("Évora→Beja distance %.1f km outside expected band 50–100", km)
	}
}

func TestDriveMins(t *testing.T) {
	if got := DriveMins(160); got < 100 || got > 140 {
		t.Fatalf("DriveMins(160) = %d, want ~120", got)
	}
}

// The two-city weekend that used to have its own bespoke check is now just the
// smallest case the multi-city planner handles, so this asserts against that
// planner instead. Kept here because Évora+Beja from Porto is the worked example
// the whole feature was designed around.
func TestPlanRoute_EvoraBejaFromPorto(t *testing.T) {
	start := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	route := multicity.Plan(multicity.Input{
		OriginName: "Porto", OriginLat: 41.1579, OriginLon: -8.6291,
		Candidates: []multicity.City{
			{Name: "Évora", Lat: 38.5714, Lon: -7.9135, Score: 70, POICount: 10},
			{Name: "Beja", Lat: 38.0153, Lon: -7.8624, Score: 55, POICount: 6},
		},
		Start: start, End: start.Add(48 * time.Hour),
		ReturnToOrigin: true,
	})

	if route.Outline == "" {
		t.Fatal("expected an outline")
	}
	if route.TotalTravelMins <= 0 {
		t.Fatalf("expected positive travel mins, got %d", route.TotalTravelMins)
	}
	// Évora and Beja are close to each other but far from Porto, so a 48-hour
	// round trip covering both is expected to exceed the travel budget and drop
	// at least one. The point is that it decides explicitly and says why.
	if len(route.Cities) == 2 && route.TravelShare > 0.35 {
		t.Errorf("kept both cities at %.0f%% travel share, above the ceiling", route.TravelShare*100)
	}
	if len(route.Cities) < 2 && len(route.Dropped) == 0 {
		t.Error("a dropped city must be explained")
	}
}
