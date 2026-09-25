package handler

import (
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
)

// SearchPOIRequest.city_name is optional: the contribute "missing place"
// search sends none, iOS sends the "nearby" placeholder with a location, and
// the trip place picker sends a real city. Each must reach a search that can
// answer it, and "nearby" must never be looked up as a city.
func TestResolveSearch(t *testing.T) {
	cases := []struct {
		name       string
		searchType string
		city       string
		lat, lon   float64
		wantMode   searchMode
		wantCity   string
	}{
		{"typed city, semantic", "semantic", "Lisbon", 0, 0, searchSemanticCity, "Lisbon"},
		{"typed city trimmed", "", "  Porto ", 0, 0, searchSemanticCity, "Porto"},
		{"no city, no location", "semantic", "", 0, 0, searchSemanticAll, ""},
		{"no city, no location, hybrid asked", "hybrid", "", 0, 0, searchSemanticAll, ""},
		{"no city, location, semantic asked", "semantic", "", 38.7, -9.1, searchHybrid, ""},
		{"no city, location, hybrid", "hybrid", "", 38.7, -9.1, searchHybrid, ""},
		{"nearby placeholder with location", "hybrid", "nearby", 38.7, -9.1, searchHybrid, ""},
		{"nearby placeholder any case, no location", "semantic", "Nearby", 0, 0, searchSemanticAll, ""},
		{"hybrid with location keeps the city", "hybrid", "Lisbon", 38.7, -9.1, searchHybrid, "Lisbon"},
		{"hybrid without location falls back to city", "hybrid", "Lisbon", 0, 0, searchSemanticCity, "Lisbon"},
		{"semantic with city and location stays in the city", "semantic", "Lisbon", 38.7, -9.1, searchSemanticCity, "Lisbon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, city := resolveSearch(tc.searchType, tc.city, tc.lat, tc.lon)
			if mode != tc.wantMode || city != tc.wantCity {
				t.Fatalf("resolveSearch(%q, %q, %v, %v) = (%v, %q), want (%v, %q)",
					tc.searchType, tc.city, tc.lat, tc.lon, mode, city, tc.wantMode, tc.wantCity)
			}
		})
	}
}

// The contribute page's missing-place search sends no city. city_name used to
// be min_len 1, so every such call failed validation before reaching here.
func TestSearchPOIRequest_CityNameIsOptional(t *testing.T) {
	for _, city := range []string{"", "nearby", "Lisbon"} {
		req := &poiv1.SearchPOIRequest{Query: "bakery", CityName: city, Latitude: 38.7, Longitude: -9.1}
		if err := protovalidate.Validate(req); err != nil {
			t.Fatalf("city %q: %v", city, err)
		}
	}
	if err := protovalidate.Validate(&poiv1.SearchPOIRequest{Query: "bakery", CityName: strings.Repeat("x", 201)}); err == nil {
		t.Fatal("an over-long city_name should still be rejected")
	}
}
