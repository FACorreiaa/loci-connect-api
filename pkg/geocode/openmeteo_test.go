package geocode

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

// Two places called Porto: Portugal's second city and a Brazilian village. This
// is the ambiguity the ranking in the city domain has to resolve, so the adapter
// must surface both, with the population and feature code that decide it.
const portoFixture = `{
  "results": [
    {"id":2735943,"name":"Porto","latitude":41.14961,"longitude":-8.61099,
     "country":"Portugal","country_code":"PT","admin1":"Porto",
     "population":249633,"feature_code":"PPLA"},
    {"id":3452925,"name":"Porto","latitude":-11.45,"longitude":-37.29,
     "country":"Brazil","country_code":"BR","admin1":"Sergipe",
     "population":1500,"feature_code":"PPL"}
  ],
  "generationtime_ms": 0.6
}`

// A miss. Note there is no "results" key at all — not an empty array.
const noResultsFixture = `{"generationtime_ms":0.41}`

func testCache(t *testing.T) cachestore.Store {
	t.Helper()
	store, err := cachestore.New(cachestore.Config{}, slog.Default())
	if err != nil {
		t.Fatalf("cachestore.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func geocodeServer(t *testing.T, body string, status int) (*OpenMeteoGeocoder, *int64, *string) {
	t.Helper()
	var hits int64
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	// No retries: a test asserting "one request reached the provider" must not
	// race the retry policy.
	client := httpx.New(httpx.Config{MaxRetries: 0})
	return NewOpenMeteo(srv.URL, "", client, testCache(t)), &hits, &lastQuery
}

func TestSearch_ParsesResults(t *testing.T) {
	g, _, _ := geocodeServer(t, portoFixture, http.StatusOK)

	places, err := g.Search(context.Background(), "Porto", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(places) != 2 {
		t.Fatalf("expected 2 places, got %d", len(places))
	}

	pt := places[0]
	if pt.Name != "Porto" || pt.Country != "Portugal" || pt.CountryCode != "PT" {
		t.Errorf("identity: got %q/%q/%q", pt.Name, pt.Country, pt.CountryCode)
	}
	if pt.Lat != 41.14961 || pt.Lon != -8.61099 {
		t.Errorf("coordinates: got %v,%v want 41.14961,-8.61099", pt.Lat, pt.Lon)
	}
	if pt.Admin1 != "Porto" {
		t.Errorf("admin1: got %q, want Porto", pt.Admin1)
	}
	if pt.Population != 249633 {
		t.Errorf("population: got %d, want 249633", pt.Population)
	}
	if pt.FeatureCode != "PPLA" {
		t.Errorf("feature code: got %q, want PPLA", pt.FeatureCode)
	}
}

// The most likely bug in the whole adapter. Open-Meteo answers a miss with 200
// and no "results" key; decoding that into a zero-length slice is correct, and
// treating it as a failure would turn every typo into a 500.
func TestSearch_NoResultsKeyIsNotAnError(t *testing.T) {
	g, _, _ := geocodeServer(t, noResultsFixture, http.StatusOK)

	places, err := g.Search(context.Background(), "Zzzqqx", 5)
	if err != nil {
		t.Fatalf("a miss must not be an error, got: %v", err)
	}
	if len(places) != 0 {
		t.Fatalf("expected no places, got %d", len(places))
	}
}

// A silent rename of any of these turns "Porto" into "every city on earth", so
// the query string is worth pinning.
func TestSearch_SendsExpectedQueryParameters(t *testing.T) {
	g, _, query := geocodeServer(t, portoFixture, http.StatusOK)

	if _, err := g.Search(context.Background(), "Porto", 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"name=Porto", "count=3", "language=en", "format=json"} {
		if !strings.Contains(*query, want) {
			t.Errorf("query %q missing %q", *query, want)
		}
	}
	if strings.Contains(*query, "apikey") {
		t.Errorf("no key was configured, but the query carried one: %q", *query)
	}
}

func TestSearch_ClampsAndDefaultsLimit(t *testing.T) {
	g, _, query := geocodeServer(t, portoFixture, http.StatusOK)

	if _, err := g.Search(context.Background(), "Porto", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(*query, "count=5") {
		t.Errorf("a zero limit should default to 5, got %q", *query)
	}
	if _, err := g.Search(context.Background(), "Lisbon", 5000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(*query, "count=100") {
		t.Errorf("limit should clamp to the provider ceiling, got %q", *query)
	}
}

// The distinction this sentinel exists for: a provider outage must not be
// reported as bad user input.
func TestSearch_ProviderFailureIsUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusTooManyRequests} {
		g, _, _ := geocodeServer(t, `{"error":true}`, status)
		_, err := g.Search(context.Background(), "Porto", 5)
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("status %d: got %v, want ErrUnavailable", status, err)
		}
	}
}

// A 400 means we asked wrongly. Retrying will not help, so it must not be
// dressed up as an outage.
func TestSearch_BadRequestIsNotUnavailable(t *testing.T) {
	g, _, _ := geocodeServer(t, `{"error":"bad parameter"}`, http.StatusBadRequest)

	_, err := g.Search(context.Background(), "Porto", 5)
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("a 400 must not map to ErrUnavailable, got %v", err)
	}
}

// The quota guard. Without it the autocomplete spends a provider request per
// keystroke.
func TestSearch_ServesSecondCallFromCache(t *testing.T) {
	g, hits, _ := geocodeServer(t, portoFixture, http.StatusOK)

	for i := 0; i < 3; i++ {
		if _, err := g.Search(context.Background(), "Porto", 5); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Errorf("provider hits: got %d, want 1", got)
	}
}

// Misses must be cached too, or a half-typed city name is a free hole in the
// rate limit.
func TestSearch_CachesNegativeResult(t *testing.T) {
	g, hits, _ := geocodeServer(t, noResultsFixture, http.StatusOK)

	for i := 0; i < 3; i++ {
		places, err := g.Search(context.Background(), "Zzzqqx", 5)
		if err != nil || len(places) != 0 {
			t.Fatalf("call %d: %v / %d places", i, err, len(places))
		}
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Errorf("provider hits: got %d, want 1", got)
	}
}

func TestSearch_EmptyNameDoesNotCallProvider(t *testing.T) {
	g, hits, _ := geocodeServer(t, portoFixture, http.StatusOK)

	places, err := g.Search(context.Background(), "   ", 5)
	if err != nil || len(places) != 0 {
		t.Fatalf("got %d places, err %v", len(places), err)
	}
	if got := atomic.LoadInt64(hits); got != 0 {
		t.Errorf("provider hits: got %d, want 0", got)
	}
}

// The free tier is non-commercial; a paid key has to actually reach the paid
// host or the subscription buys nothing.
func TestNewOpenMeteo_PicksHostFromKey(t *testing.T) {
	free := NewOpenMeteo("", "", nil, nil)
	if free.baseURL != openMeteoBaseURL {
		t.Errorf("no key: got %q, want %q", free.baseURL, openMeteoBaseURL)
	}
	paid := NewOpenMeteo("", "secret", nil, nil)
	if paid.baseURL != openMeteoCustomerBaseURL {
		t.Errorf("with key: got %q, want %q", paid.baseURL, openMeteoCustomerBaseURL)
	}
	override := NewOpenMeteo("http://localhost:9999", "secret", nil, nil)
	if override.baseURL != "http://localhost:9999" {
		t.Errorf("explicit baseURL must win, got %q", override.baseURL)
	}
}

func TestSearch_NilCacheIsSupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(portoFixture))
	}))
	t.Cleanup(srv.Close)

	g := NewOpenMeteo(srv.URL, "", httpx.New(httpx.Config{MaxRetries: 0}), nil)
	places, err := g.Search(context.Background(), "Porto", 5)
	if err != nil || len(places) != 2 {
		t.Fatalf("got %d places, err %v", len(places), err)
	}
}

var _ Forward = (*OpenMeteoGeocoder)(nil)
