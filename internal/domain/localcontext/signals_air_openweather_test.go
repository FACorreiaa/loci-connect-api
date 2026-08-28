package localcontext

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// owAir builds a forecast payload whose per-day maxima are the AQI values
// given. Three entries per day — low, peak, middling — so a fold that averaged
// instead of taking the max would fail.
func owAir(startDay int, dayPeaks ...int) string {
	var entries []string
	for i, peak := range dayPeaks {
		base := time.Date(2026, 9, startDay+i, 0, 0, 0, 0, time.UTC)
		for h, v := range []int{1, peak, max(1, peak-1)} {
			ts := base.Add(time.Duration(h*8) * time.Hour).Unix()
			const entryFmt = `{"dt":%d,"main":{"aqi":%d},"components":{"pm2_5":%d.0}}`
			entries = append(entries, fmt.Sprintf(entryFmt, ts, v, v*20))
		}
	}
	return `{"list":[` + strings.Join(entries, ",") + `]}`
}

func owAirSource(t *testing.T, body string, status int, minBand AQBand) (*OpenWeatherAirSource, *int64) {
	t.Helper()
	url, hits := serve(t, body, status)
	return NewOpenWeatherAirSource(url, "test-key", testClient(), minBand, testCache(t)), hits
}

func owWindow(from, to int) (time.Time, time.Time) {
	return time.Date(2026, 9, from, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, to, 20, 0, 0, 0, time.UTC)
}

func TestOpenWeatherAir_WarnsWhenAirIsBad(t *testing.T) {
	s, _ := owAirSource(t, owAir(2, 4, 5, 3), http.StatusOK, AQPoor)
	start, end := owWindow(2, 4)

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: 28.61, Lon: 77.21, Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
	a := got[0]
	if a.Kind != AlertAirQuality {
		t.Errorf("kind: got %q", a.Kind)
	}
	if a.Source != SourceAirQualityOW {
		t.Errorf("source: got %q", a.Source)
	}
	// AQI 5 is their worst band.
	if a.Severity != SeverityModerate {
		t.Errorf("AQI 5 maps to very poor / moderate severity, got %v", a.Severity)
	}
	if a.Located() {
		t.Error("air quality describes the destination, so it must not be located")
	}
}

func TestOpenWeatherAir_SaysNothingWhenAirIsFine(t *testing.T) {
	s, _ := owAirSource(t, owAir(2, 1, 2, 2), http.StatusOK, AQPoor)
	start, end := owWindow(2, 4)

	got, err := s.Fetch(context.Background(), SignalRequest{Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no alert for good air, got %+v", got)
	}
}

// The whole reason AQBand exists. OpenWeather's worst reading is 5; the
// European scale's "poor" starts at 60. Comparing a 5 against 60 would mean
// this source could never alert, silently.
func TestOpenWeatherAir_DoesNotUseTheEuropeanScale(t *testing.T) {
	s, _ := owAirSource(t, owAir(2, 5), http.StatusOK, AQPoor)
	start, end := owWindow(2, 2)

	got, err := s.Fetch(context.Background(), SignalRequest{Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatal("an AQI of 5 must alert; if this fails the 1-5 index is being " +
			"compared against European-AQI thresholds")
	}
}

func TestBandForOpenWeatherAQI(t *testing.T) {
	cases := map[int]AQBand{
		1: AQGood, 2: AQFair, 3: AQModerate, 4: AQPoor, 5: AQVeryPoor,
		// Out of range in either direction must not panic or wrap.
		0: AQGood, 9: AQVeryPoor, -1: AQGood,
	}
	for aqi, want := range cases {
		if got := bandForOpenWeatherAQI(aqi); got != want {
			t.Errorf("AQI %d: got %v, want %v", aqi, got, want)
		}
	}
}

// Their scale tops out at 5, so the band caps at very poor. Delhi reads 228 on
// Open-Meteo (extremely poor) and 5 here; claiming "extremely poor" from a 5
// would be inventing precision the provider does not offer.
func TestOpenWeatherAir_CapsAtVeryPoor(t *testing.T) {
	if got := bandForOpenWeatherAQI(5); got == AQExtremelyPoor {
		t.Error("a 1-5 index cannot express extremely poor")
	}
}

func TestOpenWeatherAir_OneAlertNamingTheWorstDay(t *testing.T) {
	s, _ := owAirSource(t, owAir(2, 4, 5, 4, 4, 4), http.StatusOK, AQPoor)
	start, end := owWindow(2, 6)

	got, err := s.Fetch(context.Background(), SignalRequest{Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 alert for 5 bad days, got %d", len(got))
	}
	if got[0].Date == nil || got[0].Date.Day() != 3 {
		t.Errorf("expected the worst day (3 Sep), got %v", got[0].Date)
	}
}

func TestOpenWeatherAir_UsesDailyMaxNotMean(t *testing.T) {
	// Peak 4, others 1 and 3: the mean would be below "poor".
	s, _ := owAirSource(t, owAir(2, 4), http.StatusOK, AQPoor)
	start, end := owWindow(2, 2)

	got, _ := s.Fetch(context.Background(), SignalRequest{Start: start, End: end})
	if len(got) != 1 {
		t.Error("a daily max of 4 must alert even though the mean is lower")
	}
}

func TestOpenWeatherAir_IgnoresDaysOutsideTheWindow(t *testing.T) {
	s, _ := owAirSource(t, owAir(2, 1, 1, 1, 5), http.StatusOK, AQPoor)
	start, end := owWindow(2, 2)

	got, _ := s.Fetch(context.Background(), SignalRequest{Start: start, End: end})
	if len(got) != 0 {
		t.Errorf("a bad day after the trip must not alert, got %+v", got)
	}
}

// A missing aqi must read as absent, not as zero — zero would mean "good".
func TestOpenWeatherAir_SkipsEntriesWithNoReading(t *testing.T) {
	body := `{"list":[
	 {"dt":1788307200,"main":{},"components":{}},
	 {"dt":1788336000,"main":{"aqi":5},"components":{"pm2_5":120.0}}
	]}`
	s, _ := owAirSource(t, body, http.StatusOK, AQPoor)

	got, err := s.Fetch(context.Background(), SignalRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the readable entry to count, got %d", len(got))
	}
	if !strings.Contains(got[0].Detail, "PM2.5") {
		t.Errorf("expected PM2.5 in the detail, got %q", got[0].Detail)
	}
}

// The source is only wired when a key exists; without one it must stay quiet
// rather than error, which would bench it for no reason.
func TestOpenWeatherAir_NoKeyIsQuiet(t *testing.T) {
	url, hits := serve(t, owAir(2, 5), http.StatusOK)
	s := NewOpenWeatherAirSource(url, "", testClient(), AQPoor, testCache(t))

	got, err := s.Fetch(context.Background(), SignalRequest{})
	if err != nil || len(got) != 0 {
		t.Errorf("got %d alerts, err %v", len(got), err)
	}
	if *hits != 0 {
		t.Errorf("no key should mean no upstream call, got %d", *hits)
	}
}

func TestOpenWeatherAir_SendsTheKey(t *testing.T) {
	var gotQuery string
	url, _ := serveCapturing(t, owAir(2, 5), http.StatusOK, &gotQuery)
	s := NewOpenWeatherAirSource(url, "secret", testClient(), AQPoor, testCache(t))

	if _, err := s.Fetch(context.Background(), SignalRequest{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "appid=secret") {
		t.Errorf("expected the key on the request, got %q", gotQuery)
	}
}

func TestOpenWeatherAir_ErrorsOnUnusableResponses(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"upstream 500", `{}`, http.StatusInternalServerError},
		{"bad key", `{"cod":401,"message":"Invalid API key"}`, http.StatusUnauthorized},
		{"no list", `{}`, http.StatusOK},
		{"empty list", `{"list":[]}`, http.StatusOK},
		{"entries with no readings", `{"list":[{"dt":0,"main":{}}]}`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := owAirSource(t, tt.body, tt.status, AQPoor)
			if _, err := s.Fetch(context.Background(), SignalRequest{}); err == nil {
				t.Fatal("expected an error so the Gatherer logs and skips it")
			}
		})
	}
}

func TestOpenWeatherAir_Caches(t *testing.T) {
	s, hits := owAirSource(t, owAir(2, 5), http.StatusOK, AQPoor)
	ctx := context.Background()

	for range 3 {
		if _, err := s.Fetch(ctx, SignalRequest{Lat: 38.7223, Lon: -9.1393}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if *hits != 1 {
		t.Errorf("expected 1 upstream call, got %d", *hits)
	}
}

func TestOpenWeatherAir_ImplementsSignalSource(t *testing.T) {
	var _ SignalSource = NewOpenWeatherAirSource("", "k", testClient(), AQPoor, nil)
}
