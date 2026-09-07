package poi

import (
	"math"
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The bug an agent saw as "distance_km: ~2000 for a POI in the same city".
//
// POIDetailedInfo.Distance is kilometres — the spatial repository divides
// ST_Distance by 1000 before storing it, and the MCP layer publishes the field
// as `distance_km`. The LLM enrichment path multiplied by 1000 instead, so
// every POI it produced reported metres under a kilometre label, capped by the
// radius at the ~2000 the caller observed.
func TestEnrichedDistancesAreKilometres(t *testing.T) {
	svc := &ServiceImpl{}

	// Funchal cathedral to the Monte cable car top, about 3.2 km apart.
	const (
		fromLat, fromLon = 32.6486, -16.9084
		toLat, toLon     = 32.6669, -16.8992
	)
	// The filter takes metres, matching the MCP tool's radiusM.
	const radiusMetres = 10000

	got := svc.enrichAndFilterLLMResponse([]locitypes.POIDetailedInfo{
		{Name: "Monte", Latitude: toLat, Longitude: toLon},
	}, fromLat, fromLon, radiusMetres)

	if len(got) != 1 {
		t.Fatalf("expected the POI to survive a 10 km radius, got %d results", len(got))
	}

	want := calculateDistance(fromLat, fromLon, toLat, toLon)
	if math.Abs(got[0].Distance-want) > 0.001 {
		t.Errorf("Distance = %.3f, want %.3f km", got[0].Distance, want)
	}
	// The regression itself: metres would be ~1000x larger.
	if got[0].Distance > 100 {
		t.Errorf("Distance = %.1f — that is metres in a field published as "+
			"distance_km", got[0].Distance)
	}
}

// A POI outside the radius must not survive. The radius argument is metres and
// the computed distance is kilometres, so this pins the one place where the two
// units legitimately meet.
func TestTheRadiusFilterTreatsItsArgumentAsMetres(t *testing.T) {
	svc := &ServiceImpl{}
	const fromLat, fromLon = 32.6486, -16.9084

	// Cabo Girão, ~13 km west of Funchal centre.
	far := locitypes.POIDetailedInfo{Name: "Cabo Girão", Latitude: 32.6564, Longitude: -17.0086}

	if got := svc.enrichAndFilterLLMResponse([]locitypes.POIDetailedInfo{far}, fromLat, fromLon, 5000); len(got) != 0 {
		t.Errorf("a POI 13 km away survived a 5 km radius: %+v", got)
	}
	if got := svc.enrichAndFilterLLMResponse([]locitypes.POIDetailedInfo{far}, fromLat, fromLon, 20000); len(got) != 1 {
		t.Errorf("a POI 13 km away was excluded by a 20 km radius")
	}
}

// Each filter must read the field it names. cuisine_type was compared against
// Category — "restaurant" for every restaurant, so no cuisine ever matched —
// and price_range and star_rating both against PriceLevel.
func TestFiltersReadTheFieldsTheyName(t *testing.T) {
	svc := &ServiceImpl{}

	sushi := locitypes.POIDetailedInfo{
		Name: "Sushi place", Category: "restaurant",
		CuisineType: "sushi", PriceRange: "$$", PriceLevel: "3",
	}
	italian := locitypes.POIDetailedInfo{
		Name: "Trattoria", Category: "restaurant",
		CuisineType: "italian", PriceRange: "$$$", PriceLevel: "2",
	}

	got := svc.filterRestaurants([]locitypes.POIDetailedInfo{sushi, italian}, "sushi", "")
	if len(got) != 1 || got[0].Name != "Sushi place" {
		t.Errorf("cuisine filter returned %+v", names(got))
	}

	got = svc.filterRestaurants([]locitypes.POIDetailedInfo{sushi, italian}, "", "$$$")
	if len(got) != 1 || got[0].Name != "Trattoria" {
		t.Errorf("price filter returned %+v; it must read PriceRange, not PriceLevel", names(got))
	}

	four := locitypes.POIDetailedInfo{Name: "Reid's", StarRating: "5", PriceLevel: "4"}
	three := locitypes.POIDetailedInfo{Name: "Pensão", StarRating: "3", PriceLevel: "5"}

	hotels := svc.filterHotels([]locitypes.POIDetailedInfo{four, three}, "5", "")
	if len(hotels) != 1 || hotels[0].Name != "Reid's" {
		t.Errorf("star filter returned %+v; it must read StarRating, not PriceLevel", names(hotels))
	}
}

func TestMatchesFilter(t *testing.T) {
	cases := []struct {
		field, want string
		ok          bool
		why         string
	}{
		{"sushi", "", true, "an empty request matches everything"},
		{"sushi", "sushi", true, "exact"},
		{"Italian", "italian", true, "case-insensitive: these arrive from users and models"},
		{" sushi ", "sushi", true, "surrounding space is not a difference"},
		{"sushi", "italian", false, "different values"},
		{"", "sushi", false, "an unrecorded field cannot be asserted to match"},
	}
	for _, c := range cases {
		if got := matchesFilter(c.field, c.want); got != c.ok {
			t.Errorf("matchesFilter(%q, %q) = %v, want %v — %s", c.field, c.want, got, c.ok, c.why)
		}
	}
}

func names(pois []locitypes.POIDetailedInfo) []string {
	out := make([]string, 0, len(pois))
	for _, p := range pois {
		out = append(out, p.Name)
	}
	return out
}
