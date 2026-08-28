package localcontext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const openMeteoFixture = `{
  "daily": {
    "time": ["2026-08-27","2026-08-28","2026-08-29"],
    "weather_code": [0, 63, 95],
    "temperature_2m_max": [28.4, 19.1, 17.0],
    "temperature_2m_min": [18.2, 14.0, 13.5],
    "precipitation_probability_max": [5, 80, 100]
  }
}`

func openMeteoServer(t *testing.T, body string, status int) (*OpenMeteoAdapter, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewOpenMeteoAdapter(srv.URL, "", testCache(t)), &hits
}

// The whole point of this adapter is that it needs no key, so a bare
// constructor must produce a working forecast.
func TestOpenMeteo_ParsesDailyForecast(t *testing.T) {
	a, _ := openMeteoServer(t, openMeteoFixture, http.StatusOK)

	days, err := a.Forecast(context.Background(), 38.72, -9.14, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(days) != 3 {
		t.Fatalf("expected 3 days, got %d", len(days))
	}

	if got := days[0].Date.Format("2006-01-02"); got != "2026-08-27" {
		t.Errorf("date: got %q, want 2026-08-27", got)
	}
	if days[0].HighC != 28.4 || days[0].LowC != 18.2 {
		t.Errorf("temps: got %v/%v, want 28.4/18.2", days[0].HighC, days[0].LowC)
	}
}

// Open-Meteo reports probability as a percentage; WeatherDay.PrecipProb is 0..1
// and scoreWeather multiplies by it directly. A missed /100 would make every
// forecast look like a certain downpour and silently zero the weather score.
func TestOpenMeteo_ConvertsPercentProbabilityToFraction(t *testing.T) {
	a, _ := openMeteoServer(t, openMeteoFixture, http.StatusOK)

	days, err := a.Forecast(context.Background(), 38.72, -9.14, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []float64{0.05, 0.80, 1.00}
	for i, w := range want {
		if got := days[i].PrecipProb; got < w-0.001 || got > w+0.001 {
			t.Errorf("day %d precip prob: got %v, want %v", i, got, w)
		}
	}
}

// isWet() decides a day is wet by substring-matching "rain", "storm" and
// "snow". If the WMO mapping ever returns a different vocabulary, rainy days
// score as dry and nothing else in the suite would catch it.
func TestOpenMeteo_ConditionsAreRecognisedByIsWet(t *testing.T) {
	a, _ := openMeteoServer(t, openMeteoFixture, http.StatusOK)

	days, err := a.Forecast(context.Background(), 38.72, -9.14, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if isWet(days[0]) {
		t.Errorf("code 0 (%q, 5%% precip) should not be wet", days[0].Condition)
	}
	if !isWet(days[1]) {
		t.Errorf("code 63 (%q) should be wet", days[1].Condition)
	}
	if !isWet(days[2]) {
		t.Errorf("code 95 (%q) should be wet", days[2].Condition)
	}
}

// Every code the provider documents must land on a condition the rest of the
// package understands, and thunderstorms/snow must never degrade to "Clouds".
func TestOpenMeteo_WMOCodeMappingIsExhaustive(t *testing.T) {
	cases := map[int]string{
		0: "Clear", 1: "Clear", 2: "Clouds", 3: "Clouds",
		45: "Fog", 48: "Fog",
		51: "Drizzle", 53: "Drizzle", 55: "Drizzle", 56: "Drizzle", 57: "Drizzle",
		61: "Rain", 63: "Rain", 65: "Rain", 66: "Rain", 67: "Rain",
		80: "Rain", 81: "Rain", 82: "Rain",
		71: "Snow", 73: "Snow", 75: "Snow", 77: "Snow", 85: "Snow", 86: "Snow",
		95: "Storm", 96: "Storm", 99: "Storm",
	}
	for code, want := range cases {
		if got := conditionForWMOCode(code); got != want {
			t.Errorf("code %d: got %q, want %q", code, got, want)
		}
	}
	if got := conditionForWMOCode(12345); got != "Clouds" {
		t.Errorf("unknown code should fall back to Clouds, got %q", got)
	}
}

// We always fetch a fixed 16-day horizon so one cache entry serves any window,
// then clamp. A caller asking for 2 days must not see 16.
func TestOpenMeteo_ClampsToRequestedDays(t *testing.T) {
	a, _ := openMeteoServer(t, openMeteoFixture, http.StatusOK)

	days, err := a.Forecast(context.Background(), 38.72, -9.14, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("expected 2 days, got %d", len(days))
	}
}

// A trip view can ask for the same city several times in one render; the cache
// exists so that costs one upstream call, not several.
func TestOpenMeteo_CachesByRoundedCoordinates(t *testing.T) {
	a, hits := openMeteoServer(t, openMeteoFixture, http.StatusOK)
	ctx := context.Background()

	if _, err := a.Forecast(ctx, 38.7223, -9.1393, 3); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Within ~1km, so it rounds to the same key.
	if _, err := a.Forecast(ctx, 38.7241, -9.1388, 3); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := *hits; got != 1 {
		t.Fatalf("expected 1 upstream call, got %d", got)
	}

	// A different city must not be served from that entry.
	if _, err := a.Forecast(ctx, 41.15, -8.61, 3); err != nil {
		t.Fatalf("third call: %v", err)
	}
	if got := *hits; got != 2 {
		t.Fatalf("expected 2 upstream calls after a new city, got %d", got)
	}
}

// Callers degrade gracefully on an error (handler.go returns empty context, the
// scorer treats a nil forecast as neutral), but only if we actually return one
// rather than a half-parsed forecast.
func TestOpenMeteo_ErrorsAreReturnedNotPapersOver(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"upstream 500", `{}`, http.StatusInternalServerError},
		{"upstream 429", `{}`, http.StatusTooManyRequests},
		{"empty daily block", `{"daily":{"time":[]}}`, http.StatusOK},
		{"ragged arrays", `{"daily":{"time":["2026-08-27","2026-08-28"],"weather_code":[0],"temperature_2m_max":[20],"temperature_2m_min":[10],"precipitation_probability_max":[5]}}`, http.StatusOK},
		{"bad date", `{"daily":{"time":["not-a-date"],"weather_code":[0],"temperature_2m_max":[20],"temperature_2m_min":[10],"precipitation_probability_max":[5]}}`, http.StatusOK},
		{"not json", `<html>nope</html>`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := openMeteoServer(t, tt.body, tt.status)
			days, err := a.Forecast(context.Background(), 38.72, -9.14, 3)
			if err == nil {
				t.Fatalf("expected an error, got %d days", len(days))
			}
			if days != nil {
				t.Errorf("expected no days alongside the error, got %d", len(days))
			}
		})
	}
}

// A bad response must not poison the cache — the next call should retry
// upstream rather than serve the failure forever.
func TestOpenMeteo_FailureIsNotCached(t *testing.T) {
	a, hits := openMeteoServer(t, `{}`, http.StatusInternalServerError)
	ctx := context.Background()

	_, _ = a.Forecast(ctx, 38.72, -9.14, 3)
	_, _ = a.Forecast(ctx, 38.72, -9.14, 3)
	if got := *hits; got != 2 {
		t.Fatalf("expected the failure to be retried, got %d calls", got)
	}
}

// The adapter satisfies the interface the rest of the app depends on.
func TestOpenMeteo_ImplementsWeatherAdapter(t *testing.T) {
	var _ WeatherAdapter = NewOpenMeteoAdapter("", "", nil)
}

// --- paid tier -------------------------------------------------------------
//
// Open-Meteo's free tier is non-commercial by their terms, and they name "apps
// that have subscriptions" as commercial use. Loci sells a Pro plan, so the
// paid tier has to be reachable by configuration alone — needing a code change
// to honour a licence is how licences get ignored.

func TestOpenMeteo_NoKeyUsesTheFreeHost(t *testing.T) {
	a := NewOpenMeteoAdapter("", "", nil)
	if a.baseURL != openMeteoBaseURL {
		t.Errorf("got %q, want the free host", a.baseURL)
	}
}

func TestOpenMeteo_KeySwitchesToTheCustomerHost(t *testing.T) {
	a := NewOpenMeteoAdapter("", "secret", nil)
	if a.baseURL != openMeteoCustomerBaseURL {
		t.Errorf("got %q, want the customer host", a.baseURL)
	}
}

// An explicit base URL still wins, so a self-hosted instance or a test server
// is not overridden by supplying a key.
func TestOpenMeteo_ExplicitBaseURLBeatsTheKeyDefault(t *testing.T) {
	a := NewOpenMeteoAdapter("http://localhost:9999", "secret", nil)
	if a.baseURL != "http://localhost:9999" {
		t.Errorf("got %q", a.baseURL)
	}
}

func TestOpenMeteo_SendsApiKeyWhenConfigured(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(openMeteoFixture))
	}))
	defer srv.Close()

	a := NewOpenMeteoAdapter(srv.URL, "secret-key", nil)
	if _, err := a.Forecast(context.Background(), 38.72, -9.14, 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "apikey=secret-key") {
		t.Errorf("expected the key on the request, got %q", gotQuery)
	}
}

func TestOpenMeteo_OmitsApiKeyWhenAbsent(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(openMeteoFixture))
	}))
	defer srv.Close()

	a := NewOpenMeteoAdapter(srv.URL, "", nil)
	if _, err := a.Forecast(context.Background(), 38.72, -9.14, 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(gotQuery, "apikey") {
		t.Errorf("no key configured, but one was sent: %q", gotQuery)
	}
}
