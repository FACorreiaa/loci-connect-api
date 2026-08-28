package localcontext

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveOpenMeteoForecast makes a REAL call to Open-Meteo. It needs no API
// key and is not billed, but it does reach the public internet, so normal
// `go test ./...` skips it.
//
// Run: LOCI_LIVE_WEATHER=1 go test ./internal/domain/localcontext/ -run TestLiveOpenMeteo -v
//
// It exists because the fixture tests can only prove we parse what we *think*
// the provider returns. This proves the field names, the units and the day
// horizon are still what the adapter assumes — the one thing a fixture can
// never catch when a provider changes its contract.
func TestLiveOpenMeteoForecast(t *testing.T) {
	if os.Getenv("LOCI_LIVE_WEATHER") != "1" {
		t.Skip("set LOCI_LIVE_WEATHER=1 to run the live Open-Meteo test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	a := NewOpenMeteoAdapter("", "", nil)

	// Lisbon.
	days, err := a.Forecast(ctx, 38.722252, -9.139337, 5)
	if err != nil {
		t.Fatalf("live forecast failed: %v", err)
	}
	if len(days) != 5 {
		t.Fatalf("expected 5 days, got %d", len(days))
	}

	for i, d := range days {
		if d.Date.IsZero() {
			t.Errorf("day %d has a zero date — the `time` field name or format changed", i)
		}
		// Field-name drift shows up as zero values across the board, so assert
		// the temperatures are inside a range no real city on Earth leaves.
		if d.HighC < -60 || d.HighC > 60 {
			t.Errorf("day %d high %.1f°C is out of range — units may have changed", i, d.HighC)
		}
		if d.LowC > d.HighC {
			t.Errorf("day %d low %.1f > high %.1f", i, d.LowC, d.HighC)
		}
		// The single most likely regression: a provider switching probability
		// between percent and fraction silently rescales the weather score.
		if d.PrecipProb < 0 || d.PrecipProb > 1 {
			t.Errorf("day %d precip prob %.2f is not a 0..1 fraction", i, d.PrecipProb)
		}
		if d.Condition == "" {
			t.Errorf("day %d has no condition — the weather_code field name changed", i)
		}
		t.Logf("%s  %-8s  %.1f/%.1f°C  precip %.0f%%",
			d.Date.Format("2006-01-02"), d.Condition, d.HighC, d.LowC, d.PrecipProb*100)
	}

	// Dates must be consecutive and ascending; the scorer buckets by day and
	// packing suggestions match a city's forecast to its own trip days.
	for i := 1; i < len(days); i++ {
		if !days[i].Date.After(days[i-1].Date) {
			t.Errorf("dates are not ascending at %d: %v then %v",
				i, days[i-1].Date, days[i].Date)
		}
	}
}
