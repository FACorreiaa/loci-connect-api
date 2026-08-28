package localcontext

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

// deadURL points at a closed port on loopback, so every request fails fast with
// connection refused rather than waiting on a timeout.
const deadURL = "http://127.0.0.1:1"

func fastFailClient() *httpx.Client {
	return httpx.New(httpx.Config{
		Timeout: time.Second, MaxRetries: 1,
		BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond,
		RatePerSecond: 1000, Burst: 1000, UserAgent: "loci-test/1.0",
	})
}

// The zero-key, zero-network path must work.
//
// This is the state a fresh checkout boots in and the state a network partition
// produces, and it is the one the whole design rests on: every provider here is
// a nicety layered over the itinerary, and none of them may take a trip view
// down. Asserted end-to-end rather than per-source, because the failure that
// actually happened in production was an interaction — one dead source pinning
// the whole fan-out.
func TestSignals_EverySourceUnreachable(t *testing.T) {
	client := fastFailClient()
	cache := testCache(t)
	logger := slog.New(slog.NewTextHandler(discard{}, nil))

	g := NewGatherer(
		NewBigDataCloudGeocoder(deadURL, client, cache),
		logger,
		NewHolidaySource(deadURL, client, cache),
		NewGDACSSource(deadURL, client, 500, cache),
		NewUSGSSource(deadURL, client, 500, cache),
		NewAirQualitySource(deadURL, "", client, AQPoor, cache),
	)
	g.sourceTimeout = 500 * time.Millisecond

	started := time.Now()
	alerts := g.Gather(context.Background(), 38.72, -9.14, time.Time{}, time.Time{})
	elapsed := time.Since(started)

	if len(alerts) != 0 {
		t.Errorf("expected no alerts, got %+v", alerts)
	}
	// Connection refused is immediate; anything slow here means a source is
	// waiting on a timeout it should not be.
	if elapsed > 3*time.Second {
		t.Errorf("a fully dead network should fail fast, took %v", elapsed)
	}

	// And the score still answers, treating the missing inputs as unknown
	// rather than as bad.
	score := Score(ScoreInput{
		CityName: "Lisbon", TravelMins: 60, WindowHours: 48, POICount: 5, Alerts: alerts,
	})
	if score.Score <= 0 {
		t.Errorf("the score must still answer without any provider, got %d", score.Score)
	}
	for _, f := range score.Factors {
		if f.Label == "Local disruptions" {
			t.Error("no providers means no disruptions, not a penalty")
		}
	}
}

// One dead source must not cost the healthy ones their results, and must stop
// costing latency once benched. This is the exact shape of the GDACS outage.
func TestSignals_OneDeadSourceAmongHealthyOnes(t *testing.T) {
	client := fastFailClient()
	cache := testCache(t)
	logger := slog.New(slog.NewTextHandler(discard{}, nil))

	url, _ := serve(t, nagerFixture, 200)
	healthy := NewHolidaySource(url, client, cache)
	dead := NewGDACSSource(deadURL, client, 500, cache)

	g := NewGatherer(&fakeCountry{code: "PT"}, logger, dead, healthy)
	g.sourceTimeout = 500 * time.Millisecond

	start := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC)

	got := g.Gather(context.Background(), 38.72, -9.14, start, end)
	if len(got) == 0 {
		t.Fatal("the healthy source's holidays must survive a dead sibling")
	}

	// After enough failures the dead one is benched and stops being called.
	for range benchAfterFailures {
		g.Gather(context.Background(), 38.72, -9.14, start, end)
	}
	if g.usable(SourceGDACS) {
		t.Error("the dead source should be benched")
	}
	if !g.usable(SourceHolidays) {
		t.Error("the healthy source must stay in rotation")
	}
}

// A country the holiday provider does not cover must not bench it for every
// other country — the bug that briefly removed holidays worldwide.
func TestSignals_UncoveredCountryDoesNotBenchTheSource(t *testing.T) {
	client := fastFailClient()
	logger := slog.New(slog.NewTextHandler(discard{}, nil))

	// 204 No Content, which is what Nager answers for Taiwan and India.
	url, _ := serve(t, "", 204)
	g := NewGatherer(&fakeCountry{code: "TW"}, logger,
		NewHolidaySource(url, client, testCache(t)))

	for range benchAfterFailures + 2 {
		g.Gather(context.Background(), 25.03, 121.56, time.Time{}, time.Time{})
	}
	if !g.usable(SourceHolidays) {
		t.Error("a country with no holidays on file is not a source failure")
	}
}
