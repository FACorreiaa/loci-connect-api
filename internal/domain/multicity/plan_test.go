package multicity

import (
	"strings"
	"testing"
	"time"
)

// Real coordinates, so the distances and drive times in these tests are the ones
// a user would actually get.
var (
	porto     = City{ID: "porto", Name: "Porto", Lat: 41.15, Lon: -8.61, Score: 80, POICount: 20}
	lisbon    = City{ID: "lisbon", Name: "Lisbon", Lat: 38.72, Lon: -9.14, Score: 90, POICount: 30}
	evora     = City{ID: "evora", Name: "Évora", Lat: 38.57, Lon: -7.91, Score: 70, POICount: 10}
	beja      = City{ID: "beja", Name: "Beja", Lat: 38.02, Lon: -7.86, Score: 55, POICount: 6}
	coimbra   = City{ID: "coimbra", Name: "Coimbra", Lat: 40.21, Lon: -8.43, Score: 75, POICount: 12}
	amsterdam = City{ID: "ams", Name: "Amsterdam", Lat: 52.37, Lon: 4.90, Score: 85, POICount: 40}
)

func window(hours int) (time.Time, time.Time) {
	start := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	return start, start.Add(time.Duration(hours) * time.Hour)
}

func cityNames(cities []City) []string {
	out := make([]string, len(cities))
	for i, c := range cities {
		out[i] = c.Name
	}
	return out
}

// The original case, now just the smallest one: two nearby cities, one weekend.
func TestPlan_TwoCitiesOneWeekend(t *testing.T) {
	start, end := window(48)
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{evora, beja},
		Start:      start, End: end,
	})

	if !got.Feasible {
		t.Fatalf("expected a feasible plan, got: %s", got.Outline)
	}
	if len(got.Cities) != 2 {
		t.Fatalf("expected both cities, got %v", cityNames(got.Cities))
	}
	if len(got.Days) != 2 {
		t.Errorf("expected 2 days, got %d", len(got.Days))
	}
	// One outbound leg plus one inter-city leg.
	if len(got.Legs) != 2 {
		t.Errorf("expected 2 legs, got %d", len(got.Legs))
	}
}

// The generalisation that matters: many cities across many days.
func TestPlan_FourCitiesOverTenDays(t *testing.T) {
	start, end := window(10 * 24)
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{lisbon, evora, beja, coimbra},
		Start:      start, End: end,
	})

	if !got.Feasible {
		t.Fatalf("expected a feasible plan, got: %s", got.Outline)
	}
	if len(got.Cities) != 4 {
		t.Fatalf("all four should fit ten days, got %v (dropped %+v)", cityNames(got.Cities), got.Dropped)
	}
	if len(got.Days) != 10 {
		t.Errorf("expected 10 days, got %d", len(got.Days))
	}
	// Outbound + three inter-city moves.
	if len(got.Legs) != 4 {
		t.Errorf("expected 4 legs, got %d", len(got.Legs))
	}

	// Every day must be assigned to one of the chosen cities, numbered 1..10.
	seen := map[int]bool{}
	chosen := map[string]bool{}
	for _, c := range got.Cities {
		chosen[c.Name] = true
	}
	for _, d := range got.Days {
		if !chosen[d.CityName] {
			t.Errorf("day %d assigned to %q, which is not in the route", d.DayNumber, d.CityName)
		}
		if seen[d.DayNumber] {
			t.Errorf("day %d assigned twice", d.DayNumber)
		}
		seen[d.DayNumber] = true
	}
	for n := 1; n <= 10; n++ {
		if !seen[n] {
			t.Errorf("day %d missing from the plan", n)
		}
	}
}

// Cities that cannot fit must be dropped with a reason, not silently included.
func TestPlan_DropsWhatDoesNotFitAndSaysWhy(t *testing.T) {
	start, end := window(48)
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{evora, amsterdam},
		Start:      start, End: end,
	})

	for _, c := range got.Cities {
		if c.Name == "Amsterdam" {
			t.Fatal("Amsterdam should not fit a 48-hour trip from Porto")
		}
	}
	var found bool
	for _, d := range got.Dropped {
		if d.CityName == "Amsterdam" {
			found = true
			if d.Reason == "" {
				t.Error("a dropped city must come with a reason")
			}
		}
	}
	if !found {
		t.Errorf("Amsterdam should appear in Dropped, got %+v", got.Dropped)
	}
}

// A window too short for anything is an honest "no", not a bad plan.
func TestPlan_ImpossibleWindowIsInfeasible(t *testing.T) {
	start, end := window(2)
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{amsterdam},
		Start:      start, End: end,
	})

	if got.Feasible {
		t.Fatalf("a 2-hour window to Amsterdam cannot be feasible: %s", got.Outline)
	}
	if got.Outline == "" {
		t.Error("an infeasible plan still needs to explain itself")
	}
}

// Route order should be geographically sensible, not input order.
func TestPlan_OrdersCitiesByProximity(t *testing.T) {
	start, end := window(8 * 24)
	// Deliberately worst-first input order.
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{beja, lisbon, coimbra},
		Start:      start, End: end,
	})

	if !got.Feasible || len(got.Cities) < 3 {
		t.Fatalf("expected all three, got %v", cityNames(got.Cities))
	}
	// Coimbra is much closer to Porto than Lisbon or Beja, so it must come first.
	if got.Cities[0].Name != "Coimbra" {
		t.Errorf("route = %v, expected Coimbra first (nearest to Porto)", cityNames(got.Cities))
	}
}

// The return journey has to count against the budget, or one-way plans look
// cheaper than they are.
func TestPlan_ReturnJourneyCountsAgainstTheBudget(t *testing.T) {
	start, end := window(72)
	in := Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{lisbon, evora},
		Start:      start, End: end,
	}

	oneWay := Plan(in)
	in.ReturnToOrigin = true
	roundTrip := Plan(in)

	if roundTrip.TotalTravelMins <= oneWay.TotalTravelMins {
		t.Fatalf("round trip should cost more travel: %d vs %d",
			roundTrip.TotalTravelMins, oneWay.TotalTravelMins)
	}
	if len(roundTrip.Legs) != len(oneWay.Legs)+1 {
		t.Errorf("round trip should add a homeward leg: %d vs %d legs",
			len(roundTrip.Legs), len(oneWay.Legs))
	}
}

// MaxCities is a hard cap for callers who want a shorter route than fits.
func TestPlan_RespectsMaxCities(t *testing.T) {
	start, end := window(12 * 24)
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{lisbon, evora, beja, coimbra},
		Start:      start, End: end,
		MaxCities:  2,
	})

	if len(got.Cities) != 2 {
		t.Fatalf("expected the route capped at 2, got %v", cityNames(got.Cities))
	}
	if len(got.Dropped) != 2 {
		t.Errorf("expected 2 dropped cities, got %+v", got.Dropped)
	}
}

// Calendar dates only appear when the caller supplied a start date.
func TestPlan_DatesOnlyWhenStartIsKnown(t *testing.T) {
	start, end := window(72)

	dated := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{evora}, Start: start, End: end,
	})
	for _, d := range dated.Days {
		if d.Date == nil {
			t.Fatalf("day %d has no date despite a known start", d.DayNumber)
		}
	}
	if got := dated.Days[0].Date.Format("2006-01-02"); got != "2026-09-12" {
		t.Errorf("first day = %s, want the start date", got)
	}

	relative := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{evora},
		End:        time.Time{}.Add(72 * time.Hour), // duration only, zero start
	})
	for _, d := range relative.Days {
		if d.Date != nil {
			t.Errorf("day %d should have no date on a relative plan", d.DayNumber)
		}
	}
}

// The outline is what most users will actually read, so it must name the cities,
// their day counts, and the travel cost.
func TestPlan_OutlineSummarisesTheRoute(t *testing.T) {
	start, end := window(6 * 24)
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{lisbon, evora},
		Start:      start, End: end,
	})

	for _, want := range []string{"Lisbon", "Évora", "day", "driving"} {
		if !strings.Contains(got.Outline, want) {
			t.Errorf("outline %q missing %q", got.Outline, want)
		}
	}
}

// A travel-heavy plan should say so rather than presenting itself as relaxing.
func TestPlan_WarnsWhenTravelDominates(t *testing.T) {
	start, end := window(48)
	got := Plan(Input{
		OriginName: "Porto", OriginLat: porto.Lat, OriginLon: porto.Lon,
		Candidates: []City{lisbon}, Start: start, End: end,
		ReturnToOrigin: true,
	})

	if !got.Feasible {
		t.Skip("plan infeasible; travel-share warning not reachable here")
	}
	var warned bool
	for _, w := range got.Warnings {
		if strings.Contains(w, "travel time") {
			warned = true
		}
	}
	if got.TravelShare > 0.25 && !warned {
		t.Errorf("travel share %.2f should have produced a warning, got %v", got.TravelShare, got.Warnings)
	}
}
