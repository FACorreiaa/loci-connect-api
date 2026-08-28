package localcontext

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveOpenWeatherForecast makes a REAL call to OpenWeather's 5-day/3-hour
// forecast. Needs a key and is skipped without one.
//
// Run: LOCI_LIVE_WEATHER=1 OPENWEATHER_API_KEY=... go test ./internal/domain/localcontext/ -run TestLiveOpenWeather -v
//
// This adapter predates the live-signals work, has no other tests, and has
// never been exercised against the real API — and it is now the production
// weather path. A broken forecast here does not fail loudly: it takes out the
// go-score's weather dimension, the /compare columns and packing suggestions
// together, all of which degrade quietly by design.
//
// The assertions target the assumptions that would break *silently* rather than
// the ones that would throw.
func TestLiveOpenWeatherForecast(t *testing.T) {
	if os.Getenv("LOCI_LIVE_WEATHER") != "1" {
		t.Skip("set LOCI_LIVE_WEATHER=1 to run the live OpenWeather forecast test")
	}
	key := os.Getenv("OPENWEATHER_API_KEY")
	if key == "" {
		t.Skip("OPENWEATHER_API_KEY not set; skipping")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	a := NewOpenWeatherAdapter(key)

	// Lisbon.
	days, err := a.Forecast(ctx, 38.722252, -9.139337, 5)
	if err != nil {
		t.Fatalf("live OpenWeather forecast failed: %v", err)
	}
	if len(days) == 0 {
		t.Fatal("no days parsed — the `list` field or aggregateDaily is wrong")
	}
	if len(days) > 5 {
		t.Errorf("asked for 5 days, got %d", len(days))
	}

	for i, d := range days {
		if d.Date.IsZero() {
			t.Errorf("day %d has no date — `dt` is not an epoch second as assumed", i)
		}

		// The silent killer. The adapter sends units=metric; if that ever stops
		// being honoured the values arrive in Kelvin (~290), every destination
		// reads as far too hot, and scoreWeather's comfort band penalises all of
		// them equally. Nothing errors.
		if d.HighC < -60 || d.HighC > 60 {
			t.Errorf("day %d high %.1f°C is out of range — units=metric may not be "+
				"honoured and this could be Kelvin", i, d.HighC)
		}
		if d.LowC > d.HighC {
			t.Errorf("day %d low %.1f > high %.1f", i, d.LowC, d.HighC)
		}

		// The same trap Open-Meteo has: `pop` must be a 0..1 fraction, because
		// scoreWeather multiplies the weather ceiling by it directly. A
		// percentage here would drive the weather score sharply negative.
		if d.PrecipProb < 0 || d.PrecipProb > 1 {
			t.Errorf("day %d precip prob %.2f is not a 0..1 fraction", i, d.PrecipProb)
		}

		// isWet substring-matches "rain", "storm" and "snow". An empty or
		// unexpected vocabulary means wet days score as dry.
		if d.Condition == "" {
			t.Errorf("day %d has no condition — the `weather[].main` field changed", i)
		}

		t.Logf("%s  %-14s  %.1f/%.1f°C  precip %.0f%%  wet=%v",
			d.Date.Format("2006-01-02"), d.Condition, d.HighC, d.LowC,
			d.PrecipProb*100, isWet(d))
	}

	for i := 1; i < len(days); i++ {
		if !days[i].Date.After(days[i-1].Date) {
			t.Errorf("days are not ascending at %d: %v then %v",
				i, days[i-1].Date, days[i].Date)
		}
	}

	// The forecast has to reach the scorer intact, since that is what users
	// actually see. A real forecast must never be labelled estimated.
	score := Score(ScoreInput{
		CityName: "Lisbon", Forecast: days, TravelMins: 60, WindowHours: 48, POICount: 8,
	})
	if score.HasEstimatedInputs {
		t.Error("a live forecast must not be marked estimated")
	}
	var weather ScoreFactor
	for _, f := range score.Factors {
		if f.Label == "Weather" {
			weather = f
		}
	}
	if weather.Label == "" {
		t.Fatal("no weather factor in the score")
	}
	// The neutral midpoint is what an *absent* forecast scores. Landing exactly
	// there suggests the days never reached the scorer.
	if weather.Detail == "No forecast available for this window yet" {
		t.Error("the scorer saw no forecast despite one being fetched")
	}
	t.Logf("score %d (%s) — weather %d/%d: %s",
		score.Score, score.Verdict, weather.Contribution, weather.MaxContribution, weather.Detail)
}

// The adapter refuses to run without a key rather than silently returning an
// empty forecast, which would look identical to a provider outage.
func TestOpenWeather_MissingKeyIsAnError(t *testing.T) {
	a := NewOpenWeatherAdapter("")
	if _, err := a.Forecast(context.Background(), 38.72, -9.14, 3); err == nil {
		t.Fatal("expected an error when no API key is configured")
	}
}
