package accessibility

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

func testCache(t *testing.T) cachestore.Store {
	t.Helper()
	c, err := cachestore.New(cachestore.Config{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func testClient() *httpx.Client {
	return httpx.New(httpx.Config{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
}

const sampleResponse = `{
  "type": "FeatureCollection",
  "features": [
    {"properties": {"name": "Café Next Door", "distance": 25, "sourceId": "osm",
      "accessibility": {"accessibleWith": {"wheelchair": true}}}},
    {"properties": {"name": {"en": "Museu Nacional do Azulejo"}, "distance": 18, "sourceId": "osm",
      "updatedAt": "2026-09-01T10:00:00Z",
      "accessibility": {
        "accessibleWith": {"wheelchair": false},
        "partiallyAccessibleWith": {"wheelchair": true},
        "entrances": [{"properties": {"isLevel": false, "hasFixedRamp": true}}],
        "restrooms": [{"isAccessibleWithWheelchair": true}]
      }}}
  ],
  "related": {
    "sources": {"osm": {"name": "OpenStreetMap", "licenseId": "odbl"}},
    "licenses": {"odbl": {"name": "Open Database License", "shortName": "ODbL-1.0"}}
  }
}`

func TestLookupMatchesByNameAndReadsFacts(t *testing.T) {
	var hits atomic.Int32
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotQuery = r.URL.Query()
		if r.URL.Path != "/place-infos.json" {
			t.Errorf("path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()

	src := NewAccessibilityCloud(srv.URL, "tok", testClient(), testCache(t))
	facts, err := src.Lookup(context.Background(), 38.7246, -9.1134, "Museu Nacional do Azulejo")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery.Get("appToken") != "tok" || gotQuery.Get("accuracy") != "60" || gotQuery.Get("latitude") != "38.724600" {
		t.Errorf("query = %v", gotQuery)
	}
	if facts == nil {
		t.Fatal("no match; the named museum at 18 m should match")
	}
	if facts.Wheelchair != WheelchairLimited {
		t.Errorf("partially accessible should read as limited, got %q", facts.Wheelchair)
	}
	if facts.EntranceLevel == nil || *facts.EntranceLevel || facts.EntranceRamp == nil || !*facts.EntranceRamp {
		t.Errorf("entrance facts = %v / %v", facts.EntranceLevel, facts.EntranceRamp)
	}
	if facts.AccessibleToilet == nil || !*facts.AccessibleToilet {
		t.Error("accessible toilet not read")
	}
	if facts.SourceName != "OpenStreetMap" || facts.Licence != "ODbL-1.0" {
		t.Errorf("attribution = %q / %q", facts.SourceName, facts.Licence)
	}
	if facts.ObservedAt == nil {
		t.Error("updatedAt not read")
	}

	if _, err := src.Lookup(context.Background(), 38.7246, -9.1134, "Museu Nacional do Azulejo"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("repeat lookup not cached: %d hits", hits.Load())
	}
}

// A different venue nearby must never lend its facts to ours: telling a
// wheelchair user the café next door is accessible is worse than silence.
func TestLookupRejectsNearbyDifferentName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"features":[{"properties":{"name":"Café Next Door","distance":25,
			"accessibility":{"accessibleWith":{"wheelchair":true}}}}]}`))
	}))
	defer srv.Close()

	facts, err := NewAccessibilityCloud(srv.URL, "t", testClient(), nil).
		Lookup(context.Background(), 38.7, -9.1, "Castelo de São Jorge")
	if err != nil {
		t.Fatal(err)
	}
	if facts != nil {
		t.Fatalf("matched a different venue: %+v", facts)
	}
}

func TestLookupAcceptsSameSpotWithoutName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"features":[{"properties":{"name":"","distance":4,
			"accessibility":{"accessibleWith":{"wheelchair":false}}}}]}`))
	}))
	defer srv.Close()

	facts, err := NewAccessibilityCloud(srv.URL, "t", testClient(), nil).
		Lookup(context.Background(), 38.7, -9.1, "Elevador de Santa Justa")
	if err != nil || facts == nil || facts.Wheelchair != WheelchairNo {
		t.Fatalf("facts = %+v err = %v, want wheelchair=no", facts, err)
	}
}

func TestLookupMissIsCachedAndNil(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"features":[]}`))
	}))
	defer srv.Close()

	src := NewAccessibilityCloud(srv.URL, "t", testClient(), testCache(t))
	for range 2 {
		facts, err := src.Lookup(context.Background(), 38.7, -9.1, "Nowhere")
		if err != nil || facts != nil {
			t.Fatalf("facts = %v err = %v", facts, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("miss not cached: %d hits", hits.Load())
	}
}

func TestLookupOutageIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := NewAccessibilityCloud(srv.URL, "t", testClient(), nil).Lookup(context.Background(), 38.7, -9.1, "X")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestLookupSkipsZeroCoordinates(t *testing.T) {
	src := NewAccessibilityCloud("http://127.0.0.1:1", "t", testClient(), nil)
	facts, err := src.Lookup(context.Background(), 0, 0, "Anything")
	if err != nil || facts != nil {
		t.Fatalf("zero coordinates should short-circuit, got %v / %v", facts, err)
	}
}

func TestNameSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Pastéis de Belém", "Antiga Confeitaria de Belém - Pastéis de Belém", true},
		{"MUSEU NACIONAL DO AZULEJO", "Museu Nacional do Azulejo", true},
		{"Castelo de São Jorge", "Castelo de Sao Jorge", true},
		{"Castelo de São Jorge", "Café Next Door", false},
		{"Time Out Market", "Mercado da Ribeira", false},
		{"", "Anything", false},
	}
	for _, c := range cases {
		if got := nameSimilarity(c.a, c.b) >= minNameSimilarity; got != c.want {
			t.Errorf("nameSimilarity(%q, %q) = %.2f, match %v want %v", c.a, c.b, nameSimilarity(c.a, c.b), got, c.want)
		}
	}
}
