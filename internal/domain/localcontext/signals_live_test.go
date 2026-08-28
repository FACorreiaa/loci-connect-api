package localcontext

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLiveHolidaySignals makes REAL calls to BigDataCloud and Nager.Date.
// Neither needs a key and neither is billed, but both reach the public
// internet, so normal `go test ./...` skips this.
//
// Run: LOCI_LIVE_WEATHER=1 go test ./internal/domain/localcontext/ -run TestLiveHoliday -v
//
// It proves the one thing fixtures cannot: that the live reverse geocoder still
// returns the country code the holiday API still accepts.
func TestLiveHolidaySignals(t *testing.T) {
	if os.Getenv("LOCI_LIVE_WEATHER") != "1" {
		t.Skip("set LOCI_LIVE_WEATHER=1 to run the live signals test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := NewSignalsHTTPClient()
	geo := NewBigDataCloudGeocoder("", client, nil)

	// Lisbon.
	country, err := geo.CountryCode(ctx, 38.722252, -9.139337)
	if err != nil {
		t.Fatalf("live country lookup failed: %v", err)
	}
	if country != "PT" {
		t.Fatalf("country: got %q, want PT", country)
	}

	g := NewGatherer(geo, nil, NewHolidaySource("", client, nil))

	// A window around Portugal's Republic Day, which is fixed to 5 October.
	start := time.Date(time.Now().UTC().Year(), 10, 3, 9, 0, 0, 0, time.UTC)
	end := time.Date(time.Now().UTC().Year(), 10, 5, 20, 0, 0, 0, time.UTC)

	alerts := g.Gather(ctx, 38.722252, -9.139337, start, end)
	if len(alerts) == 0 {
		t.Fatal("expected at least Republic Day in a 3-5 October window")
	}

	var found bool
	for _, a := range alerts {
		t.Logf("%-10s sev=%.2f  %s — %s", a.Kind, float64(a.Severity), a.Title, a.Detail)
		if a.Kind != AlertHoliday {
			t.Errorf("unexpected kind %q from the holiday source", a.Kind)
		}
		if a.Source != SourceHolidays {
			t.Errorf("source: got %q", a.Source)
		}
		if a.Date == nil {
			t.Error("a holiday must carry its date")
		}
		if a.Severity <= 0 || a.Severity > 1 {
			t.Errorf("severity %v is outside 0..1", a.Severity)
		}
		if a.Date != nil && a.Date.Month() == time.October && a.Date.Day() == 5 {
			found = true
		}
	}
	if !found {
		t.Error("expected an alert dated 5 October")
	}
}

// TestLiveHazardSources makes REAL calls to GDACS and USGS. Both are keyless
// and unbilled. Skipped unless LOCI_LIVE_WEATHER=1.
//
// Run: LOCI_LIVE_WEATHER=1 go test ./internal/domain/localcontext/ -run TestLiveHazard -v
//
// Asserts the response *shape* rather than any particular event, since what is
// on fire today is not a stable test fixture.
func TestLiveHazardSources(t *testing.T) {
	if os.Getenv("LOCI_LIVE_WEATHER") != "1" {
		t.Skip("set LOCI_LIVE_WEATHER=1 to run the live hazard test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	client := NewSignalsHTTPClient()

	t.Run("gdacs returns a parseable global list", func(t *testing.T) {
		// A radius large enough that the global list cannot come back empty.
		s := NewGDACSSource("", client, 20000, nil)
		got, err := s.Fetch(ctx, SignalRequest{Lat: 38.722252, Lon: -9.139337})
		if err != nil {
			t.Fatalf("live GDACS failed: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("expected some current global hazards")
		}
		for _, a := range got {
			if a.Kind != AlertHazard {
				t.Errorf("kind: got %q", a.Kind)
			}
			if !a.Located() {
				t.Errorf("hazard %q has no coordinates", a.Title)
			} else if *a.Lat < -90 || *a.Lat > 90 || *a.Lon < -180 || *a.Lon > 180 {
				// The classic [lon,lat] mix-up shows up here first.
				t.Errorf("hazard %q has impossible coordinates lat=%v lon=%v", a.Title, *a.Lat, *a.Lon)
			}
			if a.Title == "" {
				t.Error("a hazard must have a title")
			}
			if a.Severity <= 0 || a.Severity > 1 {
				t.Errorf("severity %v outside 0..1 for %q", a.Severity, a.Title)
			}
		}
		t.Logf("%d current hazards worldwide; first: %s — %s", len(got), got[0].Title, got[0].Detail)
	})

	t.Run("usgs query is accepted and parseable", func(t *testing.T) {
		s := NewUSGSSource("", client, 20000, nil)
		got, err := s.Fetch(ctx, SignalRequest{Lat: 38.722252, Lon: -9.139337})
		if err != nil {
			t.Fatalf("live USGS failed: %v", err)
		}
		// Zero significant quakes in a week is a perfectly normal answer, so
		// only the shape of whatever came back is asserted.
		for _, a := range got {
			if !a.Located() {
				t.Errorf("quake %q has no coordinates", a.Title)
			}
			if a.Date == nil {
				t.Errorf("quake %q has no date", a.Title)
			}
			t.Logf("%s — %s", a.Title, a.Detail)
		}
		t.Logf("%d significant quakes in the last %v", len(got), usgsLookback)
	})
}

// TestLiveAirQuality makes REAL calls to Open-Meteo's air-quality API. Keyless
// and unbilled. Skipped unless LOCI_LIVE_WEATHER=1.
//
// Run: LOCI_LIVE_WEATHER=1 go test ./internal/domain/localcontext/ -run TestLiveAirQuality -v
//
// Open-Meteo exposes no daily AQI aggregate (`daily=european_aqi_max` is
// rejected), so the adapter folds the hourly series itself. That fold is the
// thing most likely to break if the hourly field names or units change, and a
// fixture cannot notice.
func TestLiveAirQuality(t *testing.T) {
	if os.Getenv("LOCI_LIVE_WEATHER") != "1" {
		t.Skip("set LOCI_LIVE_WEATHER=1 to run the live air-quality test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s := NewAirQualitySource("", "", NewSignalsHTTPClient(), defaultAirQualityBand, nil)
	now := time.Now().UTC()
	start, end := now, now.Add(72*time.Hour)

	cities := []struct {
		name     string
		lat, lon float64
	}{
		{"Lisbon", 38.722252, -9.139337},
		{"Jakarta", -6.2, 106.85},
		{"Delhi", 28.61, 77.21},
	}

	for _, c := range cities {
		// The fold is exercised regardless of whether an alert is produced.
		days, err := s.forecast(ctx, c.lat, c.lon)
		if err != nil {
			t.Fatalf("%s: live air quality failed: %v", c.name, err)
		}
		if len(days) == 0 {
			t.Fatalf("%s: no days parsed from the hourly series", c.name)
		}
		for _, d := range days {
			if d.Date.IsZero() {
				t.Errorf("%s: a day has no date — the hourly `time` format changed", c.name)
			}
			// EAQI is unbounded above but a reading past 1000 means the units
			// or the field changed under us.
			if d.MaxAQI < 0 || d.MaxAQI > 1000 {
				t.Errorf("%s: implausible AQI %.0f — units may have changed", c.name, d.MaxAQI)
			}
		}
		for i := 1; i < len(days); i++ {
			if !days[i].Date.After(days[i-1].Date) {
				t.Errorf("%s: days are not ascending", c.name)
			}
		}

		alerts, err := s.Fetch(ctx, SignalRequest{Lat: c.lat, Lon: c.lon, Start: start, End: end})
		if err != nil {
			t.Fatalf("%s: fetch failed: %v", c.name, err)
		}
		// At most one alert, always — that is the contract, not an accident of
		// today's air.
		if len(alerts) > 1 {
			t.Errorf("%s: expected at most 1 alert, got %d", c.name, len(alerts))
		}

		peak := 0.0
		for _, d := range days {
			if d.MaxAQI > peak {
				peak = d.MaxAQI
			}
		}
		verdict := "no alert"
		if len(alerts) == 1 {
			verdict = fmt.Sprintf("%s (sev %.2f)", alerts[0].Title, float64(alerts[0].Severity))
			if alerts[0].Located() {
				t.Errorf("%s: an air-quality alert must not be located", c.name)
			}
		}
		t.Logf("%-8s peak EAQI %5.0f over %d days → %s", c.name, peak, len(days), verdict)
	}
}

// TestLiveFXRates makes REAL calls to Frankfurter (ECB reference rates).
// Keyless and unbilled. Skipped unless LOCI_LIVE_WEATHER=1.
//
// Run: LOCI_LIVE_WEATHER=1 go test ./internal/domain/localcontext/ -run TestLiveFX -v
//
// Asserts plausibility rather than exact values — rates move daily — but a
// rate outside these bounds means the base and quote have been swapped, which
// is the failure mode that would quietly misprice every trip.
func TestLiveFXRates(t *testing.T) {
	if os.Getenv("LOCI_LIVE_WEATHER") != "1" {
		t.Skip("set LOCI_LIVE_WEATHER=1 to run the live FX test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	a := NewFXAdapter("", NewSignalsHTTPClient(), nil)

	rates, unsupported, err := a.Rates(ctx, "EUR", []string{"USD", "GBP", "JPY", "VND"})
	if err != nil {
		t.Fatalf("live FX failed: %v", err)
	}
	if len(rates) != 3 {
		t.Fatalf("expected 3 supported rates, got %d", len(rates))
	}
	if len(unsupported) != 1 || unsupported[0] != "VND" {
		t.Errorf("expected VND reported unsupported, got %v", unsupported)
	}

	// Bounds wide enough to survive years of drift, tight enough to catch an
	// inverted pair (EUR/USD inverted would be ~0.86, EUR/JPY ~0.005).
	bounds := map[string][2]float64{
		"USD": {0.5, 3},
		"GBP": {0.3, 2},
		"JPY": {50, 500},
	}
	for _, r := range rates {
		if r.Base != "EUR" {
			t.Errorf("base: got %q", r.Base)
		}
		if r.AsOf.IsZero() {
			t.Errorf("%s has no publication date", r.Quote)
		}
		b, ok := bounds[r.Quote]
		if !ok {
			continue
		}
		if r.Rate < b[0] || r.Rate > b[1] {
			t.Errorf("EUR/%s = %v is outside [%v, %v] — the pair may be inverted",
				r.Quote, r.Rate, b[0], b[1])
		}
		t.Logf("1 EUR = %.4f %s (as of %s)", r.Rate, r.Quote, r.AsOf.Format("2006-01-02"))
	}

	// The country convenience path must resolve to something priceable.
	if got := CurrencyForCountry("JP"); got != "JPY" {
		t.Errorf("JP should resolve to JPY, got %q", got)
	}
}

// TestLiveOpenWeatherAirQuality makes a REAL call to OpenWeather's air
// pollution API. Needs a key and is skipped without one.
//
// Run: LOCI_LIVE_WEATHER=1 OPENWEATHER_API_KEY=... go test ./internal/domain/localcontext/ -run TestLiveOpenWeatherAir -v
//
// This one matters more than the other live tests. The endpoint paths were
// confirmed against the live API (they answer 401, not 404), but the response
// *shape* was written from documentation that could not be fetched — their docs
// site is JS-rendered. Everything below is therefore unverified until this test
// runs green against a real key. The GDACS `iscurrent` bug earlier in this work
// was exactly this failure mode: a field typed from assumption rather than from
// a live payload.
func TestLiveOpenWeatherAirQuality(t *testing.T) {
	if os.Getenv("LOCI_LIVE_WEATHER") != "1" {
		t.Skip("set LOCI_LIVE_WEATHER=1 to run the live OpenWeather air test")
	}
	key := os.Getenv("OPENWEATHER_API_KEY")
	if key == "" {
		t.Skip("OPENWEATHER_API_KEY not set; skipping")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s := NewOpenWeatherAirSource("", key, NewSignalsHTTPClient(), defaultAirQualityBand, nil)

	// Delhi: reliably poor, so this exercises an alert rather than silence.
	days, err := s.forecast(ctx, 28.61, 77.21)
	if err != nil {
		t.Fatalf("live OpenWeather air quality failed: %v", err)
	}
	if len(days) == 0 {
		t.Fatal("no days parsed — the `list` field name or `dt` handling is wrong")
	}

	for _, d := range days {
		if d.Date.IsZero() {
			t.Errorf("a day has no date — `dt` is not an epoch second as assumed")
		}
		// Their index is 1-5. Anything else means we are reading the wrong
		// field, most likely a European AQI from somewhere.
		if d.MaxAQI < 1 || d.MaxAQI > 5 {
			t.Errorf("AQI %.0f is outside OpenWeather's 1-5 scale — wrong field?", d.MaxAQI)
		}
		t.Logf("%s  aqi %.0f (%s)  pm2.5 %.0f",
			d.Date.Format("2006-01-02"), d.MaxAQI,
			bandForOpenWeatherAQI(int(d.MaxAQI)).Label(), d.MaxPM25)
	}
	for i := 1; i < len(days); i++ {
		if !days[i].Date.After(days[i-1].Date) {
			t.Errorf("days are not ascending at %d", i)
		}
	}

	now := time.Now().UTC()
	alerts, err := s.Fetch(ctx, SignalRequest{
		Lat: 28.61, Lon: 77.21, Start: now, End: now.Add(72 * time.Hour),
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if len(alerts) > 1 {
		t.Errorf("at most one alert per trip is the contract, got %d", len(alerts))
	}
	for _, a := range alerts {
		if a.Located() {
			t.Error("an air-quality alert must not be located")
		}
		t.Logf("alert: %s — %s (sev %.2f)", a.Title, a.Detail, float64(a.Severity))
	}
}
