package geocode

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

// TestLiveOpenMeteoGeocode makes a REAL call to Open-Meteo's geocoding search.
// It needs no API key and is not billed, but it does reach the public internet,
// so a normal `go test ./...` skips it.
//
// Run: LOCI_LIVE_GEOCODE=1 go test ./pkg/geocode/ -run TestLiveOpenMeteo -v
//
// The fixture tests can only prove we parse what we *think* the provider
// returns. This proves the field names and the miss behaviour are still what the
// adapter assumes, which is the one thing a fixture can never catch when a
// provider changes its contract.
func TestLiveOpenMeteoGeocode(t *testing.T) {
	if os.Getenv("LOCI_LIVE_GEOCODE") != "1" {
		t.Skip("set LOCI_LIVE_GEOCODE=1 to run the live Open-Meteo geocoding test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	g := NewOpenMeteo("", "", httpx.New(httpx.Config{}), nil)

	places, err := g.Search(ctx, "Porto", 5)
	if err != nil {
		t.Fatalf("live search: %v", err)
	}
	if len(places) == 0 {
		t.Fatal("no results for Porto")
	}

	var found bool
	for _, p := range places {
		if p.CountryCode != "PT" || !strings.EqualFold(p.Name, "Porto") {
			continue
		}
		found = true
		// Porto sits at roughly 41.15 N, 8.61 W. A degree of slack catches a
		// latitude/longitude swap or a sign error without being brittle about
		// the provider nudging its centroid.
		if p.Lat < 40 || p.Lat > 42 {
			t.Errorf("latitude %v is not Porto", p.Lat)
		}
		if p.Lon < -9.5 || p.Lon > -7.5 {
			t.Errorf("longitude %v is not Porto", p.Lon)
		}
		if p.Population <= 0 {
			t.Error("population is missing, so same-name ranking has nothing to sort on")
		}
		if !strings.HasPrefix(p.FeatureCode, "PPL") {
			t.Errorf("feature code %q would be filtered out as a non-populated place", p.FeatureCode)
		}
		if p.Country == "" {
			t.Error("country name is missing, so persisted rows would fall back to Unknown")
		}
	}
	if !found {
		t.Errorf("Porto, PT was not among the results: %+v", places)
	}

	// The trap this adapter exists around: a miss is 200 with no "results" key,
	// and must read as an empty answer rather than a failure.
	miss, err := g.Search(ctx, "Zzzqqxwv", 5)
	if err != nil {
		t.Fatalf("a miss must not be an error: %v", err)
	}
	if len(miss) != 0 {
		t.Errorf("expected no results, got %d", len(miss))
	}
}
