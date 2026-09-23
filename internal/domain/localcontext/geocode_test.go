package localcontext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

func geocoder(t *testing.T, body string, status int) (*BigDataCloudGeocoder, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client := httpx.New(httpx.Config{
		Timeout: 2 * time.Second, MaxRetries: 1,
		BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond,
		RatePerSecond: 1000, Burst: 1000, UserAgent: "loci-test/1.0",
	})
	return NewBigDataCloudGeocoder(srv.URL, client, testCache(t)), &hits
}

func TestGeocoder_ReturnsCountryCode(t *testing.T) {
	g, _ := geocoder(t, `{"countryCode":"PT","countryName":"Portugal"}`, http.StatusOK)

	got, err := g.CountryCode(context.Background(), 38.722252, -9.139337)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "PT" {
		t.Errorf("got %q, want PT", got)
	}
}

func TestGeocoder_UppercasesAndTrims(t *testing.T) {
	g, _ := geocoder(t, `{"countryCode":"  pt  "}`, http.StatusOK)

	got, err := g.CountryCode(context.Background(), 38.7, -9.1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "PT" {
		t.Errorf("got %q, want PT", got)
	}
}

// Open ocean genuinely has no country. Returning an error would make the
// Gatherer log a failure for a correct answer.
func TestGeocoder_EmptyCountryIsNotAnError(t *testing.T) {
	g, _ := geocoder(t, `{"countryCode":""}`, http.StatusOK)

	got, err := g.CountryCode(context.Background(), 0, -30)
	if err != nil {
		t.Fatalf("expected no error at sea, got %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// The country a coordinate sits in does not change, so the cache key is
// deliberately coarse (~11km): a whole city's lookups collapse to one call.
func TestGeocoder_CachesCoarsely(t *testing.T) {
	g, hits := geocoder(t, `{"countryCode":"PT"}`, http.StatusOK)
	ctx := context.Background()

	// Two points a couple of kilometres apart inside Lisbon.
	//
	// Note the key is a rounded grid, not a radius, so two nearby points either
	// side of a cell boundary do miss. That is deliberate and harmless: a miss
	// costs one extra lookup of a value that cannot be wrong, and a radius
	// index would be far more machinery than a country lookup deserves.
	if _, err := g.CountryCode(ctx, 38.7223, -9.1393); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := g.CountryCode(ctx, 38.7401, -9.1201); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *hits != 1 {
		t.Errorf("expected 1 upstream call for nearby points, got %d", *hits)
	}

	// Far enough away to be a different country.
	if _, err := g.CountryCode(ctx, 40.4168, -3.7038); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *hits != 2 {
		t.Errorf("expected a second call for a distant point, got %d", *hits)
	}
}

func TestGeocoder_UpstreamFailureIsAnError(t *testing.T) {
	g, _ := geocoder(t, `{}`, http.StatusInternalServerError)

	if _, err := g.CountryCode(context.Background(), 38.7, -9.1); err == nil {
		t.Fatal("expected an error so the Gatherer can log and continue")
	}
}

func TestGeocoder_ImplementsCountryResolver(t *testing.T) {
	var _ CountryResolver = NewBigDataCloudGeocoder("", httpx.New(httpx.Config{}), nil)
}

func TestGeocoder_Place(t *testing.T) {
	g, hits := geocoder(t, `{"countryCode":"PT","countryName":"Portugal","city":"Viana do Castelo","locality":"Santa Maria Maior","principalSubdivision":"Viana do Castelo District"}`, http.StatusOK)

	p, err := g.Place(context.Background(), 41.69, -8.83)
	if err != nil {
		t.Fatal(err)
	}
	want := Place{Locality: "Viana do Castelo", Region: "Viana do Castelo District", CountryCode: "PT", CountryName: "Portugal"}
	if p != want {
		t.Errorf("place = %+v, want %+v", p, want)
	}
	if _, err := g.Place(context.Background(), 41.69, -8.83); err != nil || atomic.LoadInt64(hits) != 1 {
		t.Errorf("second lookup should be cached: hits=%d err=%v", atomic.LoadInt64(hits), err)
	}
	if _, err := g.Place(context.Background(), 41.79, -8.83); err != nil || atomic.LoadInt64(hits) != 2 {
		t.Errorf("0.1° away is another town key: hits=%d", atomic.LoadInt64(hits))
	}
}

func TestGeocoder_PlaceFallsBackToLocality(t *testing.T) {
	g, _ := geocoder(t, `{"countryCode":"pt","countryName":"Portugal","city":"","locality":"Afife","principalSubdivision":""}`, http.StatusOK)
	p, err := g.Place(context.Background(), 41.77, -8.86)
	if err != nil || p.Locality != "Afife" || p.CountryCode != "PT" {
		t.Errorf("place = %+v err=%v", p, err)
	}
}
