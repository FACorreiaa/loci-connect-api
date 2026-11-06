package routing

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

var (
	castle = LatLon{Lat: 38.7139, Lon: -9.1334}
	river  = LatLon{Lat: 38.7075, Lon: -9.1365}
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

func TestRouteWheelchairRequestAndResponse(t *testing.T) {
	var hits atomic.Int32
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":812.4,"duration":702.1,"ascent":41.2},
			"warnings":[{"code":3,"message":"There may be restrictions on some roads"}]}]}`))
	}))
	defer srv.Close()

	ors := NewOpenRouteService(srv.URL, "test-key", testClient(), testCache(t), nil)
	opts := Options{Profile: ProfileWheelchair, MaxInclinePct: 6, MaxSlopedKerbM: 0.03, AvoidSteps: true, PavedOnly: true}
	leg, err := ors.Route(context.Background(), castle, river, opts)
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/v2/directions/wheelchair/json" || gotAuth != "test-key" {
		t.Errorf("path %q auth %q", gotPath, gotAuth)
	}
	coords := gotBody["coordinates"].([]any)
	first := coords[0].([]any)
	if first[0].(float64) != castle.Lon || first[1].(float64) != castle.Lat {
		t.Errorf("coordinates must be [lon, lat], got %v", first)
	}
	if gotBody["elevation"] != true {
		t.Error("elevation not requested, so ascent would be missing")
	}
	o := gotBody["options"].(map[string]any)
	restr := o["profile_params"].(map[string]any)["restrictions"].(map[string]any)
	if restr["maximum_incline"].(float64) != 6 || restr["maximum_sloped_kerb"].(float64) != 0.03 || restr["surface_type"] != "paved" {
		t.Errorf("restrictions not sent: %v", restr)
	}
	if av := o["avoid_features"].([]any); len(av) != 1 || av[0] != "steps" {
		t.Errorf("avoid_features = %v", av)
	}

	if leg.DistanceM != 812.4 || leg.DurationS != 702.1 || leg.AscentM != 41.2 {
		t.Errorf("leg = %+v", leg)
	}
	if len(leg.Warnings) != 1 {
		t.Errorf("warnings = %v", leg.Warnings)
	}

	// Second call is served from cache.
	if _, err := ors.Route(context.Background(), castle, river, opts); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("cache miss on repeat: %d provider hits", hits.Load())
	}
}

func TestRouteWalkingSendsNoWheelchairRestrictions(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":100,"duration":80}}]}`))
	}))
	defer srv.Close()

	ors := NewOpenRouteService(srv.URL, "k", testClient(), nil, nil)
	if _, err := ors.Route(context.Background(), castle, river, Options{MaxInclinePct: 6}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v2/directions/foot-walking/json" {
		t.Errorf("empty profile should default to walking, got %q", gotPath)
	}
	if _, ok := gotBody["options"]; ok {
		t.Errorf("walking request carried wheelchair options: %v", gotBody["options"])
	}
}

func TestRouteNoRouteIsAnAnswerAndIsCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":2009,"message":"Route could not be found"}}`))
	}))
	defer srv.Close()

	ors := NewOpenRouteService(srv.URL, "k", testClient(), testCache(t), nil)
	for range 2 {
		if _, err := ors.Route(context.Background(), castle, river, Options{Profile: ProfileWheelchair}); !errors.Is(err, ErrNoRoute) {
			t.Fatalf("err = %v, want ErrNoRoute", err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("no-route answer not cached: %d hits", hits.Load())
	}
}

func TestRouteOutageIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ors := NewOpenRouteService(srv.URL, "k", testClient(), testCache(t), nil)
	_, err := ors.Route(context.Background(), castle, river, Options{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, ErrNoRoute) {
		t.Fatal("an outage must not read as 'no route'")
	}
}

func TestRouteUnknown4xxIsUnavailableNotNoRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Access to this API has been disallowed"}`))
	}))
	defer srv.Close()

	ors := NewOpenRouteService(srv.URL, "bad-key", testClient(), nil, nil)
	if _, err := ors.Route(context.Background(), castle, river, Options{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestRouteBudget(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":1,"duration":1}}]}`))
	}))
	defer srv.Close()

	ors := NewOpenRouteService(srv.URL, "k", testClient(), nil, NewDailyBudget(1))
	if _, err := ors.Route(context.Background(), castle, river, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ors.Route(context.Background(), river, castle, Options{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("over budget: err = %v, want ErrUnavailable", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("budget did not stop the call: %d hits", hits.Load())
	}
}

func TestDailyBudgetResetsAtUTCMidnight(t *testing.T) {
	now := time.Date(2026, 11, 6, 23, 59, 0, 0, time.UTC)
	b := NewDailyBudget(1)
	b.now = func() time.Time { return now }
	if !b.Take() || b.Take() {
		t.Fatal("budget of 1 should allow exactly one call")
	}
	now = now.Add(2 * time.Minute)
	if !b.Take() {
		t.Fatal("budget did not reset on a new UTC day")
	}
	var nilBudget *DailyBudget
	if !nilBudget.Take() || !NewDailyBudget(0).Take() {
		t.Fatal("nil or zero budget means unlimited")
	}
}

func TestLegCacheKeySeparatesOptions(t *testing.T) {
	a := legCacheKey(castle, river, Options{Profile: ProfileWheelchair, MaxInclinePct: 6})
	if a == legCacheKey(castle, river, Options{Profile: ProfileWheelchair, MaxInclinePct: 3}) {
		t.Error("incline must split the key")
	}
	if a == legCacheKey(river, castle, Options{Profile: ProfileWheelchair, MaxInclinePct: 6}) {
		t.Error("direction must split the key (ascent differs)")
	}
	jitter := LatLon{Lat: castle.Lat + 0.00001, Lon: castle.Lon}
	if a != legCacheKey(jitter, river, Options{Profile: ProfileWheelchair, MaxInclinePct: 6}) {
		t.Error("~1 m of jitter should hit the same entry")
	}
}
