package transit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
)

var verdi = LatLon{Lat: 41.4105, Lon: 2.1560}

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
	return httpx.New(httpx.Config{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, RatePerSecond: 1000, Burst: 1000})
}

// fakeMap answers every query with one canned body and records the queries.
type fakeMap struct {
	body    string
	status  int
	hits    atomic.Int32
	queries []string
}

func (f *fakeMap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits.Add(1)
	raw, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(raw))
	f.queries = append(f.queries, form.Get("data"))
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	_, _ = io.WriteString(w, f.body)
}

func newMap(t *testing.T, body string) (*fakeMap, *Overpass) {
	t.Helper()
	f := &fakeMap{body: body}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, NewOverpass(OverpassConfig{BaseURL: srv.URL}, testClient(), testCache(t), nil)
}

// The shape the public server returned for a point in Gràcia, cut down: the
// everyday places, the delimiter, then each stop followed by its routes.
const graciaBody = `{"elements":[
 {"type":"node","id":1,"lat":41.4107,"lon":2.1562,"tags":{"amenity":"cafe","name":"Cafe Flanders"}},
 {"type":"node","id":2,"lat":41.4120,"lon":2.1580,"tags":{"amenity":"restaurant","name":"La Pubilla"}},
 {"type":"way","id":3,"center":{"lat":41.4110,"lon":2.1550},"tags":{"shop":"supermarket","name":"Bonpreu"}},
 {"type":"node","id":4,"lat":41.4112,"lon":2.1571,"tags":{"amenity":"pharmacy"}},
 {"type":"node","id":5,"lat":41.4100,"lon":2.1565,"tags":{"amenity":"bank","name":"Not counted"}},
 {"type":"delim","id":1},
 {"type":"node","id":10,"lat":41.4089,"lon":2.1535,"tags":{"highway":"bus_stop","name":"Trav de Dalt - La Granja","route_ref":"24 116 D40 H6 N0"}},
 {"type":"relation","id":11,"tags":{"type":"route","route":"bus","ref":"H6","name":"H6: Zona Universitària => Onze de Setembre"}},
 {"type":"relation","id":12,"tags":{"type":"route","route":"bus","ref":"V17"}},
 {"type":"node","id":13,"lat":41.4091,"lon":2.1537,"tags":{"highway":"bus_stop","name":"Trav de Dalt - la Granja","route_ref":"24;116"}},
 {"type":"node","id":20,"lat":41.4066,"lon":2.1527,"tags":{"railway":"station","station":"subway","name":"Lesseps"}},
 {"type":"relation","id":21,"tags":{"type":"route","route":"subway","ref":"L3"}},
 {"type":"relation","id":22,"tags":{"type":"route","route":"bus","ref":"22"}},
 {"type":"node","id":30,"lat":41.4100,"lon":2.1560,"tags":{"highway":"bus_stop"}},
 {"type":"relation","id":31,"tags":{"type":"route","route":"bus","ref":"99"}}
]}`

func TestAroundReadsStopsLinesAndPlaces(t *testing.T) {
	f, o := newMap(t, graciaBody)
	got, err := o.Around(context.Background(), verdi)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Stops) != 2 {
		t.Fatalf("stops = %+v, want the bus stop (both sides as one) and the metro; the nameless stop is dropped", got.Stops)
	}
	bus, metro := got.Stops[0], got.Stops[1]
	if bus.Mode != ModeBus || bus.Name != "Trav de Dalt - La Granja" {
		t.Errorf("nearest = %+v", bus)
	}
	if strings.Join(bus.Lines, ",") != "24,116,D40,H6,N0,V17" {
		t.Errorf("bus lines = %v, want its own tags and its routes, sorted by letter then number", bus.Lines)
	}
	if metro.Mode != ModeMetro || strings.Join(metro.Lines, ",") != "L3" {
		t.Errorf("metro = %+v, want L3 only: the bus outside the station is not its line", metro)
	}
	if bus.DistanceM <= 0 || bus.DistanceM > busRadiusM || metro.DistanceM < bus.DistanceM {
		t.Errorf("distances bus=%d metro=%d", bus.DistanceM, metro.DistanceM)
	}

	want := map[string]int{"eat": 2, "groceries": 1, "pharmacies": 1}
	if len(got.Highlights) != len(want) {
		t.Fatalf("highlights = %+v", got.Highlights)
	}
	for _, h := range got.Highlights {
		if want[h.Kind] != h.Count {
			t.Errorf("%s = %d, want %d", h.Kind, h.Count, want[h.Kind])
		}
	}
	if got.Highlights[0].Kind != "eat" || got.Highlights[0].NearestName != "Cafe Flanders" {
		t.Errorf("first highlight = %+v", got.Highlights[0])
	}
	if got.FetchedAt.IsZero() {
		t.Error("no fetch time")
	}

	// A second ask a few metres away is the same answer from the cache.
	if _, err := o.Around(context.Background(), LatLon{Lat: 41.41051, Lon: 2.15601}); err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", f.hits.Load())
	}
	if !strings.Contains(f.queries[0], "around:800,41.41050,2.15600") || !strings.Contains(f.queries[0], "make delim") {
		t.Errorf("query = %s", f.queries[0])
	}
}

func TestAroundUnavailable(t *testing.T) {
	off := NewOverpass(OverpassConfig{}, testClient(), nil, nil)
	if off.Enabled() {
		t.Error("no URL should be off")
	}
	if _, err := off.Around(context.Background(), verdi); !errors.Is(err, ErrUnavailable) {
		t.Errorf("unconfigured err = %v", err)
	}

	f, o := newMap(t, "")
	f.status = http.StatusBadGateway
	if _, err := o.Around(context.Background(), verdi); !errors.Is(err, ErrUnavailable) {
		t.Errorf("busy server err = %v", err)
	}

	_, spent := newMap(t, graciaBody)
	spent.budget = routing.NewDailyBudget(1)
	if _, err := spent.Around(context.Background(), verdi); err != nil {
		t.Fatal(err)
	}
	if _, err := spent.Around(context.Background(), LatLon{Lat: 41.39, Lon: 2.17}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("spent budget err = %v", err)
	}
}

func TestAroundDoesNotCacheAnAnswerTheServerGaveUpOn(t *testing.T) {
	f, o := newMap(t, `{"elements":[],"remark":"runtime error: Query timed out in \"query\" at line 3 after 13 seconds."}`)
	if _, err := o.Around(context.Background(), verdi); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	f.body = graciaBody
	got, err := o.Around(context.Background(), verdi)
	if err != nil || len(got.Stops) == 0 {
		t.Errorf("after recovery: %+v %v", got, err)
	}
}

func TestFallbackServer(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	up := httptest.NewServer(&fakeMap{body: graciaBody})
	defer up.Close()
	o := NewOverpass(OverpassConfig{BaseURL: down.URL, FallbackURL: up.URL}, testClient(), nil, nil)
	got, err := o.Around(context.Background(), verdi)
	if err != nil || len(got.Stops) != 2 {
		t.Errorf("got %+v err %v", got, err)
	}
}

func TestFindLodging(t *testing.T) {
	body := `{"elements":[
 {"type":"node","id":1,"lat":41.3980,"lon":2.1560,"tags":{"tourism":"hotel","name":"Hotel Casa Fuster","stars":"5","addr:street":"Passeig de Gràcia","addr:housenumber":"132","addr:postcode":"08008","addr:city":"Barcelona"}},
 {"type":"way","id":2,"center":{"lat":41.3981,"lon":2.1561},"tags":{"tourism":"hotel","name":"Casa Fuster"}},
 {"type":"node","id":3,"lat":41.3800,"lon":2.1700,"tags":{"tourism":"hostel","name":"Casa Fuster Annex Old Town"}}
]}`
	f, o := newMap(t, body)
	centre := LatLon{Lat: 41.3874, Lon: 2.1686}
	p, ok, err := o.FindLodging(context.Background(), "Casa Fuster", centre)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if p.Lat != 41.3980 || p.Stars != "5" || p.Address != "Passeig de Gràcia 132, 08008 Barcelona" {
		t.Errorf("place = %+v", p)
	}
	if !strings.Contains(f.queries[0], `[name~"f[uùúûü]st[eèéêë]r",i]`) {
		t.Errorf("query = %s", f.queries[0])
	}

	// A name made only of words every hotel has is not looked up at all.
	if _, ok, _ := o.FindLodging(context.Background(), "Hotel Apartments", centre); ok {
		t.Error("generic name matched")
	}
}

func TestFindLodgingNeverGuesses(t *testing.T) {
	centre := LatLon{Lat: 41.3874, Lon: 2.1686}
	cases := map[string]string{
		"a hotel sharing the street's name": `{"elements":[{"type":"node","id":1,"lat":41.40,"lon":2.15,"tags":{"tourism":"hotel","name":"Hotel Verdi"}}]}`,
		"two different places fit equally": `{"elements":[
			{"type":"node","id":1,"lat":41.40,"lon":2.15,"tags":{"tourism":"hotel","name":"Verdi 254"}},
			{"type":"node","id":2,"lat":41.38,"lon":2.17,"tags":{"tourism":"apartment","name":"Verdi 254"}}]}`,
		"nothing found": `{"elements":[]}`,
	}
	for name, body := range cases {
		_, o := newMap(t, body)
		if p, ok, err := o.FindLodging(context.Background(), "Verdi 254", centre); ok || err != nil {
			t.Errorf("%s: got %+v ok=%v err=%v", name, p, ok, err)
		}
	}
}

func TestFindAddress(t *testing.T) {
	centre := LatLon{Lat: 41.3874, Lon: 2.1686}
	f, o := newMap(t, `{"elements":[{"type":"way","id":1,"center":{"lat":41.4105,"lon":2.1560},"tags":{"addr:street":"Carrer de Verdi","addr:housenumber":"254","addr:postcode":"08024"}}]}`)
	p, ok, err := o.FindAddress(context.Background(), "Verdi", "254", centre)
	if err != nil || !ok || p.Lat != 41.4105 || p.Address != "Carrer de Verdi 254, 08024" {
		t.Errorf("place = %+v ok=%v err=%v", p, ok, err)
	}
	if !strings.Contains(f.queries[0], `["addr:housenumber"~"^254$",i]`) {
		t.Errorf("query = %s", f.queries[0])
	}

	_, two := newMap(t, `{"elements":[
		{"type":"node","id":1,"lat":41.41,"lon":2.15,"tags":{"addr:street":"Carrer de Verdi","addr:housenumber":"254"}},
		{"type":"node","id":2,"lat":41.36,"lon":2.11,"tags":{"addr:street":"Passatge Verdi","addr:housenumber":"254"}}]}`)
	if _, ok, _ := two.FindAddress(context.Background(), "Verdi", "254", centre); ok {
		t.Error("two streets with that number: no telling which")
	}

	// A number that is not a house number never reaches the query.
	for _, bad := range []string{"", `1"](if:1)`, "254-256", "abc"} {
		_, o := newMap(t, `{"elements":[]}`)
		if _, ok, _ := o.FindAddress(context.Background(), "Verdi", bad, centre); ok {
			t.Errorf("number %q matched", bad)
		}
	}
}

func TestNamesCannotBreakOutOfTheQuery(t *testing.T) {
	f, o := newMap(t, `{"elements":[]}`)
	_, _, _ = o.FindLodging(context.Background(), `Verdi"];node(1);out;//`, LatLon{Lat: 41.38, Lon: 2.16})
	if len(f.queries) != 1 || strings.Count(f.queries[0], `"`) != 4 || strings.Contains(f.queries[0], "node(1)") {
		t.Errorf("query = %s", f.queries)
	}
}

func TestWalkEstimateAndLinks(t *testing.T) {
	for dist, want := range map[int]int{0: 1, 60: 1, 400: 7, 800: 13, 1500: 25} {
		if got := EstimateWalkMinutes(dist); got != want {
			t.Errorf("%d m = %d min, want %d", dist, got, want)
		}
	}
	if SuggestedMode(900) != "walk" || SuggestedMode(2400) != "transit" {
		t.Error("suggested mode")
	}
	from, to := LatLon{Lat: 41.4105, Lon: 2.156}, LatLon{Lat: 41.4036, Lon: 2.1744}
	g := GoogleTransitURL(from, to)
	if g != "https://www.google.com/maps/dir/?api=1&destination=41.40360%2C2.17440&origin=41.41050%2C2.15600&travelmode=transit" {
		t.Errorf("google = %s", g)
	}
	a := AppleTransitURL(from, to)
	if a != "https://maps.apple.com/?daddr=41.40360%2C2.17440&dirflg=r&saddr=41.41050%2C2.15600" {
		t.Errorf("apple = %s", a)
	}
	if d := DistanceM(from, to); d < 1600 || d > 1800 {
		t.Errorf("distance = %d", d)
	}
}
