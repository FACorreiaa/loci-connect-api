package localcontext

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

// testCache builds a real in-memory TieredStore, so tests exercise the same
// cache path production uses rather than a stub that cannot catch a
// serialisation bug.
func testCache(t *testing.T) *signalCache {
	t.Helper()
	store, err := cachestore.New(cachestore.Config{}, slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return newSignalCache(store, nil)
}

func testClient() *httpx.Client {
	return httpx.New(httpx.Config{
		Timeout: 2 * time.Second, MaxRetries: 1,
		BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond,
		RatePerSecond: 1000, Burst: 1000, UserAgent: "loci-test/1.0",
	})
}

func serve(t *testing.T, body string, status int) (string, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

// Lisbon is 38.72,-9.14. Sintra (~25km) is near; Madrid (~500km) and Reykjavik
// are progressively further.
const gdacsFixture = `{"features":[
 {"geometry":{"coordinates":[-9.39,38.80]},"properties":{
   "eventtype":"WF","alertlevel":"Orange","name":"Wildfire in Portugal","country":"Portugal",
   "fromdate":"2026-09-01T00:00:00","todate":"2026-09-05T00:00:00","iscurrent":true,
   "severitydata":{"severitytext":"12000 ha burnt"}}},
 {"geometry":{"coordinates":[-21.94,64.15]},"properties":{
   "eventtype":"VO","alertlevel":"Red","name":"Volcano in Iceland","country":"Iceland",
   "fromdate":"2026-09-01T00:00:00","todate":"2026-09-05T00:00:00","iscurrent":true,
   "severitydata":{"severitytext":"eruption ongoing"}}},
 {"geometry":{"coordinates":[-9.20,38.75]},"properties":{
   "eventtype":"FL","alertlevel":"Green","name":"Flood in Portugal","country":"Portugal",
   "fromdate":"2025-01-01T00:00:00","todate":"2025-01-05T00:00:00","iscurrent":false,
   "severitydata":{"severitytext":"minor"}}}
]}`

func lisbonWindow() (time.Time, time.Time) {
	return time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC)
}

// The headline signal: a wildfire near where you are going.
func TestGDACS_SurfacesNearbyCurrentHazards(t *testing.T) {
	url, _ := serve(t, gdacsFixture, http.StatusOK)
	s := NewGDACSSource(url, testClient(), 500, testCache(t))
	start, end := lisbonWindow()

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: 38.72, Lon: -9.14, Start: start, End: end})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected only the nearby in-window wildfire, got %d: %+v", len(got), got)
	}
	a := got[0]
	if a.Kind != AlertHazard {
		t.Errorf("kind: got %q", a.Kind)
	}
	if a.Source != SourceGDACS {
		t.Errorf("source: got %q", a.Source)
	}
	if !a.Located() {
		t.Fatal("a hazard must be located so it can become a map pin")
	}
	// GeoJSON is [lon, lat]; reading it backwards would put Portugal in the
	// Indian Ocean and the distance filter would silently drop everything.
	if *a.Lat < 38 || *a.Lat > 39 || *a.Lon > -9 || *a.Lon < -10 {
		t.Errorf("coordinates look swapped: lat=%v lon=%v", *a.Lat, *a.Lon)
	}
	if !strings.Contains(a.Detail, "km away") {
		t.Errorf("detail should state the distance, got %q", a.Detail)
	}
	if !strings.Contains(a.Detail, "12000 ha") {
		t.Errorf("detail should carry the severity text, got %q", a.Detail)
	}
}

func TestGDACS_FiltersByDistance(t *testing.T) {
	url, _ := serve(t, gdacsFixture, http.StatusOK)
	start, end := lisbonWindow()

	// Iceland's volcano is thousands of km from Lisbon.
	near := NewGDACSSource(url, testClient(), 500, testCache(t))
	got, _ := near.Fetch(context.Background(), SignalRequest{Lat: 38.72, Lon: -9.14, Start: start, End: end})
	for _, a := range got {
		if strings.Contains(a.Title, "Iceland") {
			t.Error("Iceland must not appear within 500km of Lisbon")
		}
	}

	// From Reykjavik it is the nearby one.
	fromIceland := NewGDACSSource(url, testClient(), 500, testCache(t))
	got2, _ := fromIceland.Fetch(context.Background(), SignalRequest{Lat: 64.15, Lon: -21.94, Start: start, End: end})
	if len(got2) != 1 || !strings.Contains(got2[0].Title, "Iceland") {
		t.Errorf("expected the Iceland volcano from Reykjavik, got %+v", got2)
	}
}

// A flood in January 2025 must not penalise a trip in September 2026.
func TestGDACS_FiltersByWindowOverlap(t *testing.T) {
	url, _ := serve(t, gdacsFixture, http.StatusOK)
	s := NewGDACSSource(url, testClient(), 500, testCache(t))
	start, end := lisbonWindow()

	got, _ := s.Fetch(context.Background(), SignalRequest{Lat: 38.72, Lon: -9.14, Start: start, End: end})
	for _, a := range got {
		if strings.Contains(a.Title, "Flood") {
			t.Error("a hazard from last year must not match this year's window")
		}
	}
}

// Leaning on GDACS's own triage is the point — they weigh magnitude against
// population exposure, which is what decides whether anything is disrupted.
func TestGDACSSeverity_MapsAlertLevel(t *testing.T) {
	tests := map[string]Severity{
		"Red": SeverityMajor, "red": SeverityMajor,
		"Orange": SeverityModerate, "orange": SeverityModerate,
		"Green": SeverityMinor,
		// Most of the global list is green at any moment; treating unknown as
		// major would put every destination permanently under a warning.
		"": SeverityMinor, "chartreuse": SeverityMinor,
	}
	for in, want := range tests {
		if got := gdacsSeverity(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

// One global list, fetched once, filtered locally — five cities on a compare
// page must cost one upstream call.
func TestGDACS_CachesTheGlobalList(t *testing.T) {
	url, hits := serve(t, gdacsFixture, http.StatusOK)
	s := NewGDACSSource(url, testClient(), 500, testCache(t))
	start, end := lisbonWindow()
	ctx := context.Background()

	for _, city := range [][2]float64{{38.72, -9.14}, {41.15, -8.61}, {64.15, -21.94}} {
		if _, err := s.Fetch(ctx, SignalRequest{Lat: city[0], Lon: city[1], Start: start, End: end}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if *hits != 1 {
		t.Errorf("expected 1 upstream call for 3 cities, got %d", *hits)
	}
}

func TestGDACS_UpstreamFailureIsAnError(t *testing.T) {
	url, _ := serve(t, `{}`, http.StatusInternalServerError)
	s := NewGDACSSource(url, testClient(), 500, testCache(t))
	if _, err := s.Fetch(context.Background(), SignalRequest{Lat: 38.72, Lon: -9.14}); err == nil {
		t.Fatal("expected an error so the Gatherer logs and skips it")
	}
}

// --- USGS ------------------------------------------------------------------

func usgsFixture(agoHours int) string {
	when := time.Now().UTC().Add(-time.Duration(agoHours) * time.Hour).UnixMilli()
	return fmt.Sprintf(`{"features":[
	 {"geometry":{"coordinates":[-9.30,38.60,10]},"properties":{
	   "mag":5.4,"place":"20 km SW of Lisbon","time":%d,"alert":null,"title":"M 5.4"}}
	]}`, when)
}

func TestUSGS_ProducesLocatedQuakeAlerts(t *testing.T) {
	url, _ := serve(t, usgsFixture(6), http.StatusOK)
	s := NewUSGSSource(url, testClient(), 500, testCache(t))

	got, err := s.Fetch(context.Background(), SignalRequest{Lat: 38.72, Lon: -9.14})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d alerts", len(got))
	}
	a := got[0]
	if a.Kind != AlertHazard || a.Source != SourceUSGS {
		t.Errorf("kind/source: %q/%q", a.Kind, a.Source)
	}
	if !a.Located() {
		t.Error("a quake must be located")
	}
	if !strings.Contains(a.Title, "5.4") {
		t.Errorf("title should carry the magnitude, got %q", a.Title)
	}
	if !strings.Contains(a.Detail, "hours ago") {
		t.Errorf("detail should say how recent it was, got %q", a.Detail)
	}
}

// PAGER accounts for depth, population and building stock; magnitude alone does
// not. A M6 far offshore disrupts nothing, a M5 under a city disrupts a lot.
func TestQuakeSeverity_PagerOverridesMagnitude(t *testing.T) {
	if got := quakeSeverity(4.6, "red"); got != SeverityMajor {
		t.Errorf("a red PAGER alert must be major regardless of magnitude, got %v", got)
	}
	if got := quakeSeverity(4.6, "yellow"); got != SeverityModerate {
		t.Errorf("yellow PAGER should be moderate, got %v", got)
	}
	if got := quakeSeverity(6.5, ""); got != SeverityMajor {
		t.Errorf("M6.5 with no PAGER should be major, got %v", got)
	}
	if got := quakeSeverity(5.2, ""); got != SeverityModerate {
		t.Errorf("M5.2 should be moderate, got %v", got)
	}
	if got := quakeSeverity(4.6, ""); got != SeverityMinor {
		t.Errorf("M4.6 should be minor, got %v", got)
	}
}

// --- shared ----------------------------------------------------------------

func TestWindowsOverlap(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name           string
		aS, aE, bS, bE time.Time
		want           bool
	}{
		{"identical", d(1), d(3), d(1), d(3), true},
		{"partial overlap", d(1), d(3), d(2), d(5), true},
		{"a entirely before b", d(1), d(2), d(4), d(6), false},
		{"a entirely after b", d(8), d(9), d(4), d(6), false},
		{"touching at a boundary", d(1), d(3), d(3), d(5), true},
		// A hazard with no dates is still a hazard.
		{"unbounded event", time.Time{}, time.Time{}, d(4), d(6), true},
		{"unbounded window", d(1), d(3), time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowsOverlap(tt.aS, tt.aE, tt.bS, tt.bE); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseGDACSTime(t *testing.T) {
	if got := parseGDACSTime("2026-09-01T12:30:00"); got.IsZero() || got.Year() != 2026 || got.Month() != time.September {
		t.Errorf("got %v", got)
	}
	if got := parseGDACSTime("2026-09-01"); got.IsZero() {
		t.Error("a date-only value should parse")
	}
	if got := parseGDACSTime("nonsense"); !got.IsZero() {
		t.Errorf("garbage should yield the zero time, got %v", got)
	}
	if got := parseGDACSTime(""); !got.IsZero() {
		t.Errorf("empty should yield the zero time, got %v", got)
	}
}
