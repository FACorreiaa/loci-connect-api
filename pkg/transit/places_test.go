package transit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
)

var lisbon = LatLon{Lat: 38.7223, Lon: -9.1393}

// A cut-down answer for a Lisbon plan: the tile museum as a node and as its
// outline, a bus stop and a bus line named after it, Belém Tower under its
// Portuguese name with an English one beside it, a market, and a bakery
// chain with two branches.
const lisbonBody = `{"elements":[
 {"type":"node","id":10,"lat":38.7249,"lon":-9.1135,"tags":{"tourism":"museum","name":"Museu Nacional do Azulejo","name:en":"National Tile Museum","wikidata":"Q1542372"}},
 {"type":"way","id":11,"center":{"lat":38.7251,"lon":-9.1137},"tags":{"building":"yes","tourism":"museum","name":"Museu Nacional do Azulejo","opening_hours":"Tu-Su 10:00-18:00"}},
 {"type":"node","id":12,"lat":38.7262,"lon":-9.1120,"tags":{"highway":"bus_stop","name":"Museu Nacional do Azulejo"}},
 {"type":"relation","id":13,"tags":{"type":"route","route":"bus","name":"Museu Nacional do Azulejo"}},
 {"type":"way","id":20,"center":{"lat":38.6916,"lon":-9.2160},"tags":{"historic":"tower","name":"Torre de Belém","name:en":"Belém Tower","opening_hours":"Tu-Su 09:30-18:00"}},
 {"type":"way","id":30,"center":{"lat":38.7069,"lon":-9.1459},"tags":{"amenity":"marketplace","name":"Time Out Market Lisboa","opening_hours":"Mo-Su 10:00-24:00"}},
 {"type":"node","id":40,"lat":38.7100,"lon":-9.1400,"tags":{"shop":"bakery","name":"A Padaria Portuguesa"}},
 {"type":"node","id":41,"lat":38.7400,"lon":-9.1500,"tags":{"shop":"bakery","name":"A Padaria Portuguesa"}},
 {"type":"node","id":50,"lat":38.7139,"lon":-9.1335,"tags":{"name":"Castelo de São Jorge"}},
 {"type":"node","id":60,"lat":38.7150,"lon":-9.1300,"tags":{"tourism":"museum","name":"Museu do Fado","alt_name":"Fado Museum;Casa do Fado"}}
]}`

func TestFindPlaces(t *testing.T) {
	f, o := newMap(t, lisbonBody)
	names := []string{
		"Museu Nacional do Azulejo",
		"Belém Tower",
		"Time Out Market",
		"A Padaria Portuguesa",
		"Castelo de São Jorge",
		"Casa do Fado",
		"Oceanário de Lisboa",
		"??",
	}
	got, err := o.FindPlaces(context.Background(), names, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(names) {
		t.Fatalf("got %d answers for %d names", len(got), len(names))
	}

	// The node and the outline are one place; the outline has the hours. The
	// bus stop and the bus line with the same name do not make it a tie.
	azulejo := got[0]
	if !azulejo.Found || azulejo.Place.OSMType != "way" || azulejo.Place.OSMID != 11 ||
		azulejo.Place.OpeningHours != "Tu-Su 10:00-18:00" || azulejo.Place.Kind != "museum" || azulejo.Place.Confidence != 1 {
		t.Errorf("azulejo = %+v", azulejo)
	}

	// Found by its English name, reported under the map's own name and place.
	belem := got[1]
	if !belem.Found || belem.Place.Name != "Torre de Belém" || belem.Place.Lat != 38.6916 || belem.Place.Confidence != 1 {
		t.Errorf("belem = %+v", belem)
	}

	// The map's name adds the city.
	market := got[2]
	if !market.Found || market.Place.OSMID != 30 || market.Place.Confidence != confidenceMapAddsOne {
		t.Errorf("market = %+v", market)
	}

	// Two branches of a chain: the name cannot say which.
	if got[3].Found {
		t.Errorf("chain matched one branch: %+v", got[3])
	}
	// A name on something that is not tagged as a place to visit.
	if got[4].Found {
		t.Errorf("untagged entry matched: %+v", got[4])
	}
	// One of several alternative names.
	if fado := got[5]; !fado.Found || fado.Place.Name != "Museu do Fado" {
		t.Errorf("fado = %+v", fado)
	}
	// Not on the map at all.
	if got[6].Found {
		t.Errorf("absent place matched: %+v", got[6])
	}
	// No word to look for.
	if got[7].Found {
		t.Errorf("wordless name matched: %+v", got[7])
	}

	if f.hits.Load() != 1 {
		t.Errorf("asked the map %d times, want one query for the whole plan", f.hits.Load())
	}
	q := f.queries[0]
	for _, want := range []string{"z[uùúûü]l[eèéêë]j[oòóôõö]", "b[eèéêë]l[eèéêë]m", "name:en", "around:12000,38.72230,-9.13930"} {
		if !strings.Contains(q, want) {
			t.Errorf("query lacks %q:\n%s", want, q)
		}
	}
	// "Museu" and "Tower" are poor search words and must not be the ones used.
	for _, generic := range []string{`"museu"`, `"tower"`, `"m[uùúûü]s[eèéêë][uùúûü]"`} {
		if strings.Contains(q, generic) {
			t.Errorf("query searches on a generic word %s:\n%s", generic, q)
		}
	}
}

func TestFindPlacesCachesHitsAndMisses(t *testing.T) {
	f, o := newMap(t, lisbonBody)
	names := []string{"Belém Tower", "Oceanário de Lisboa"}
	first, err := o.FindPlaces(context.Background(), names, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	second, err := o.FindPlaces(context.Background(), names, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 1 {
		t.Errorf("asked the map %d times, want 1", f.hits.Load())
	}
	if first[0] != second[0] || first[1] != second[1] || !second[0].Found || second[1].Found {
		t.Errorf("cached answers differ: %+v vs %+v", first, second)
	}

	// Only the name not seen before is asked for.
	if _, err := o.FindPlaces(context.Background(), []string{"Belém Tower", "Museu do Fado"}, lisbon); err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 2 {
		t.Fatalf("asked the map %d times, want 2", f.hits.Load())
	}
	if q := f.queries[1]; strings.Contains(q, "b[eèéêë]l[eèéêë]m") || !strings.Contains(q, "f[aàáâãäå]d[oòóôõö]") {
		t.Errorf("second query should ask only for the new name:\n%s", q)
	}
}

func TestFindPlacesSplitsLongLists(t *testing.T) {
	f, o := newMap(t, `{"elements":[]}`)
	names := make([]string, 0, maxPlacesPerQuery+5)
	for i := range maxPlacesPerQuery + 5 {
		names = append(names, "Lugar "+strings.Repeat(string(rune('a'+i%26)), 4+i/26))
	}
	if _, err := o.FindPlaces(context.Background(), names, lisbon); err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 2 {
		t.Errorf("asked the map %d times, want 2", f.hits.Load())
	}
}

func TestFindPlacesFailureIsNotCached(t *testing.T) {
	f := &fakeMap{status: http.StatusBadGateway}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	o := NewOverpass(OverpassConfig{BaseURL: srv.URL}, testClient(), testCache(t), nil)

	got, err := o.FindPlaces(context.Background(), []string{"Belém Tower"}, lisbon)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if len(got) != 1 || got[0].Found {
		t.Errorf("got = %+v, want one not-found answer", got)
	}

	// The map comes back: the name is asked for again, not served as a miss.
	f.status, f.body = 0, lisbonBody
	got, err = o.FindPlaces(context.Background(), []string{"Belém Tower"}, lisbon)
	if err != nil || !got[0].Found {
		t.Errorf("after recovery: %+v, %v", got, err)
	}
}

func TestFindPlacesRespectsTheBudget(t *testing.T) {
	f := &fakeMap{body: lisbonBody}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	o := NewOverpass(OverpassConfig{BaseURL: srv.URL}, testClient(), testCache(t), routing.NewDailyBudget(1))

	if _, err := o.FindPlaces(context.Background(), []string{"Belém Tower"}, lisbon); err != nil {
		t.Fatal(err)
	}
	if _, err := o.FindPlaces(context.Background(), []string{"Museu do Fado"}, lisbon); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable once the budget is spent", err)
	}
	if f.hits.Load() != 1 {
		t.Errorf("asked the map %d times, want 1", f.hits.Load())
	}
}

func TestFindPlacesQueryCannotBeBrokenOutOf(t *testing.T) {
	f, o := newMap(t, `{"elements":[]}`)
	_, _ = o.FindPlaces(context.Background(), []string{`Belém"];node(1);out;//`}, lisbon)
	if len(f.queries) != 1 {
		t.Fatalf("queries = %d", len(f.queries))
	}
	if q := f.queries[0]; strings.Contains(q, "node(1)") || strings.Contains(q, "//") || strings.Count(q, "\n") != 4 {
		t.Errorf("name leaked into the query:\n%s", q)
	}
}

func TestNameConfidence(t *testing.T) {
	tests := []struct {
		given, cand string
		want        float64
	}{
		{"Museu Nacional do Azulejo", "Museu Nacional do Azulejo", confidenceExact},
		{"museu nacional azulejo", "Museu Nacional do Azulejo", confidenceExact},
		{"Casa Fuster", "Casa Fuster Barcelona", confidenceMapAddsOne},
		{"Time Out Market Lisboa", "Time Out Market", 0},
		{"Verdi", "Hotel Verdi", 0},
		{"Museu do Fado", "Museu do Oriente", 0},
		{"National Tile Museum", "National Museum", 0},
		{"Belém", "", 0},
	}
	for _, tt := range tests {
		if got := nameConfidence(tt.given, tt.cand); got != tt.want {
			t.Errorf("nameConfidence(%q, %q) = %v, want %v", tt.given, tt.cand, got, tt.want)
		}
	}
}

func TestPlaceWord(t *testing.T) {
	for name, want := range map[string]string{
		"Museu Nacional do Azulejo": "azulejo",
		"Belém Tower":               "belem",
		"Mercado Central":           "central",
		"Old Market":                "market",
		"Sé":                        "",
		"":                          "",
	} {
		if got := placeWord(name); got != want {
			t.Errorf("placeWord(%q) = %q, want %q", name, got, want)
		}
	}
}
