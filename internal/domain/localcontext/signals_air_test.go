package localcontext

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// hourlyAQ builds a response whose per-day maxima are the values given.
// Each day gets three hours: a clean one, the peak, and a middling one — so a
// test that accidentally averaged instead of taking the max would fail.
func hourlyAQ(startDay int, dayPeaks ...float64) string {
	var times, aqi, pm []string
	for i, peak := range dayPeaks {
		day := fmt.Sprintf("2026-09-%02d", startDay+i)
		for h, v := range []float64{peak * 0.2, peak, peak * 0.5} {
			times = append(times, fmt.Sprintf("%q", fmt.Sprintf("%sT%02d:00", day, h*8)))
			aqi = append(aqi, fmt.Sprintf("%.1f", v))
			pm = append(pm, fmt.Sprintf("%.1f", v*0.6))
		}
	}
	return fmt.Sprintf(`{"hourly":{"time":[%s],"european_aqi":[%s],"pm2_5":[%s]}}`,
		strings.Join(times, ","), strings.Join(aqi, ","), strings.Join(pm, ","))
}

func airSource(t *testing.T, body string, status int, threshold float64) (*AirQualitySource, *int64) {
	t.Helper()
	url, hits := serve(t, body, status)
	return NewAirQualitySource(url, testClient(), threshold), hits
}

func sepWindow(from, to int) (time.Time, time.Time) {
	return time.Date(2026, 9, from, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, to, 20, 0, 0, 0, time.UTC)
}

// Good air is not news. Saying so on every clean trip trains users to ignore
// the alert list.
func TestAirQuality_SaysNothingWhenAirIsFine(t *testing.T) {
	// Lisbon's real readings while this was written were 27-37.
	s, _ := airSource(t, hourlyAQ(2, 27, 33, 37), http.StatusOK, 60)
	start, end := sepWindow(2, 4)

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: 38.72, Lon: -9.14, Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no alert for fair air, got %+v", got)
	}
}

func TestAirQuality_WarnsWhenAirIsBad(t *testing.T) {
	// Jakarta's real readings were 86-95.
	s, _ := airSource(t, hourlyAQ(2, 86, 95, 90), http.StatusOK, 60)
	start, end := sepWindow(2, 4)

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: -6.2, Lon: 106.85, Start: start, End: end})
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
	if a.Source != SourceAirQuality {
		t.Errorf("source: got %q", a.Source)
	}
	if a.Severity != SeverityModerate {
		t.Errorf("AQI 95 (very poor) should be moderate, got %v", a.Severity)
	}
	if !strings.Contains(a.Detail, "95") {
		t.Errorf("detail should name the worst reading, got %q", a.Detail)
	}
	if !strings.Contains(a.Detail, "PM2.5") {
		t.Errorf("detail should include PM2.5, got %q", a.Detail)
	}
}

// The scorer charges per alert, so a five-day trip through smoke must not be
// penalised five times for one fact about one place.
func TestAirQuality_EmitsOneAlertNamingTheWorstDay(t *testing.T) {
	s, _ := airSource(t, hourlyAQ(2, 70, 150, 80, 95, 65), http.StatusOK, 60)
	start, end := sepWindow(2, 6)

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: 28.61, Lon: 77.21, Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 alert for 5 bad days, got %d", len(got))
	}
	if got[0].Severity != SeverityMajor {
		t.Errorf("the worst day (150) should drive severity, got %v", got[0].Severity)
	}
	if got[0].Date == nil || got[0].Date.Day() != 3 {
		t.Errorf("the alert should be dated the worst day (3 Sep), got %v", got[0].Date)
	}
}

// A day that is clean all morning and hazardous all afternoon is exactly the
// day you want warning about; averaging hides it.
func TestAirQuality_UsesDailyMaxNotMean(t *testing.T) {
	// One day peaking at 90: mean of (18, 90, 45) is 51, below the threshold.
	s, _ := airSource(t, hourlyAQ(2, 90), http.StatusOK, 60)
	start, end := sepWindow(2, 2)

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: 0, Lon: 0, Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a max of 90 must alert even though the mean is below threshold, got %d", len(got))
	}
}

// A bad day outside the trip is not the traveller's problem.
func TestAirQuality_IgnoresDaysOutsideTheWindow(t *testing.T) {
	// Day 2 is clean, day 5 is terrible; the trip is day 2 only.
	s, _ := airSource(t, hourlyAQ(2, 30, 30, 30, 200), http.StatusOK, 60)
	start, end := sepWindow(2, 2)

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: 0, Lon: 0, Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a bad day after the trip must not alert, got %+v", got)
	}
}

func TestAirQuality_ThresholdIsConfigurable(t *testing.T) {
	body := hourlyAQ(2, 45)
	start, end := sepWindow(2, 2)
	ctx := context.Background()

	strict, _ := airSource(t, body, http.StatusOK, 40)
	if got, _ := strict.Fetch(ctx, SignalRequest{Start: start, End: end}); len(got) != 1 {
		t.Errorf("threshold 40 should alert on 45, got %d", len(got))
	}

	lax, _ := airSource(t, body, http.StatusOK, 60)
	if got, _ := lax.Fetch(ctx, SignalRequest{Start: start, End: end}); len(got) != 0 {
		t.Errorf("threshold 60 should stay quiet on 45, got %d", len(got))
	}
}

// Air quality describes the destination itself, so a pin would land on top of
// the city marker and add nothing. Hazards are located; this is not.
func TestAirQuality_AlertIsNotLocated(t *testing.T) {
	s, _ := airSource(t, hourlyAQ(2, 120), http.StatusOK, 60)
	start, end := sepWindow(2, 2)

	got, _ := s.Fetch(context.Background(), SignalRequest{Lat: 28.61, Lon: 77.21, Start: start, End: end})
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Located() {
		t.Error("an air-quality alert must not carry coordinates")
	}
}

func TestAirQuality_CachesByRoundedCoordinates(t *testing.T) {
	s, hits := airSource(t, hourlyAQ(2, 90), http.StatusOK, 60)
	start, end := sepWindow(2, 2)
	ctx := context.Background()

	for range 3 {
		if _, err := s.Fetch(ctx, SignalRequest{Lat: 38.7223, Lon: -9.1393, Start: start, End: end}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if *hits != 1 {
		t.Errorf("expected 1 upstream call, got %d", *hits)
	}
}

// The series carries gaps; a null hour must not be read as zero or crash.
func TestAirQuality_ToleratesNullHours(t *testing.T) {
	body := `{"hourly":{"time":["2026-09-02T00:00","2026-09-02T08:00","2026-09-02T16:00"],
	 "european_aqi":[null,95,null],"pm2_5":[null,null,60]}}`
	s, _ := airSource(t, body, http.StatusOK, 60)
	start, end := sepWindow(2, 2)

	got, err := s.Fetch(context.Background(), SignalRequest{Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the non-null reading to count, got %d", len(got))
	}
	if !strings.Contains(got[0].Detail, "95") {
		t.Errorf("got %q", got[0].Detail)
	}
}

func TestAirQuality_ErrorsOnUnusableResponses(t *testing.T) {
	start, end := sepWindow(2, 2)
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"upstream 500", `{}`, http.StatusInternalServerError},
		// Open-Meteo answers a bad variable name with a 400 and an error body.
		{"bad request", `{"error":true,"reason":"invalid variable"}`, http.StatusBadRequest},
		{"no hourly block", `{}`, http.StatusOK},
		{"empty series", `{"hourly":{"time":[]}}`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := airSource(t, tt.body, tt.status, 60)
			if _, err := s.Fetch(context.Background(), SignalRequest{Start: start, End: end}); err == nil {
				t.Fatal("expected an error so the Gatherer logs and skips it")
			}
		})
	}
}

// The labels are the European AQI's own vocabulary, so what a user reads here
// matches any official air-quality site they check.
func TestAQIBandsAndSeverity(t *testing.T) {
	bands := []struct {
		aqi   float64
		label string
	}{
		{5, "Good"},
		{27, "Fair"},
		{45, "Moderate"},
		{65, "Poor"},
		{90, "Very poor"},
		{228, "Extremely poor"},
	}
	for _, b := range bands {
		if got := aqiBandLabel(b.aqi); got != b.label {
			t.Errorf("AQI %.0f: got %q, want %q", b.aqi, got, b.label)
		}
	}

	sev := map[float64]Severity{
		65: SeverityMinor, 79: SeverityMinor,
		80: SeverityModerate, 99: SeverityModerate,
		100: SeverityMajor, 228: SeverityMajor,
	}
	for aqi, want := range sev {
		if got := aqiSeverity(aqi); got != want {
			t.Errorf("AQI %.0f: got %v, want %v", aqi, got, want)
		}
	}
}

func TestAirQuality_ImplementsSignalSource(t *testing.T) {
	var _ SignalSource = NewAirQualitySource("", testClient(), 0)
}
