package transit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
)

var lisbon = LatLon{Lat: 38.7223, Lon: -9.1393}

// sights asks for each name as somewhere to see, the default for a plan.
func sights(names ...string) []PlaceQuery {
	out := make([]PlaceQuery, len(names))
	for i, n := range names {
		out[i] = PlaceQuery{Name: n}
	}
	return out
}

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
 {"type":"way","id":30,"center":{"lat":38.7069,"lon":-9.1459},"tags":{"amenity":"marketplace","name":"Time Out Market Lisboa","wikidata":"Q28226115","opening_hours":"Mo-Su 10:00-24:00"}},
 {"type":"node","id":40,"lat":38.7100,"lon":-9.1400,"tags":{"shop":"bakery","name":"A Padaria Portuguesa"}},
 {"type":"node","id":41,"lat":38.7400,"lon":-9.1500,"tags":{"shop":"bakery","name":"A Padaria Portuguesa"}},
 {"type":"node","id":50,"lat":38.7139,"lon":-9.1335,"tags":{"name":"Castelo de São Jorge"}},
 {"type":"node","id":60,"lat":38.7150,"lon":-9.1300,"tags":{"tourism":"museum","name":"Museu do Fado","alt_name":"Fado Museum;Casa do Fado"}}
]}`

func TestFindPlaces(t *testing.T) {
	f, o := newMap(t, lisbonBody)
	names := sights(
		"Museu Nacional do Azulejo",
		"Belém Tower",
		"Time Out Market",
		"A Padaria Portuguesa",
		"Castelo de São Jorge",
		"Casa do Fado",
		"Oceanário de Lisboa",
		"??",
	)
	got, err := o.FindPlaces(context.Background(), names, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(names) {
		t.Fatalf("got %d answers for %d names", len(got), len(names))
	}

	// The node and the outline are one place. The bus stop and the bus line
	// with the same name do not make it a tie.
	azulejo := got[0]
	if !azulejo.Found || azulejo.Place.Kind != "museum" || azulejo.Place.Confidence != 1 || azulejo.Place.Wikidata != "Q1542372" {
		t.Errorf("azulejo = %+v", azulejo)
	}

	// Found by its English name, reported under the map's own name and place.
	belem := got[1]
	if !belem.Found || belem.Place.Name != "Torre de Belém" || belem.Place.Lat != 38.6916 || belem.Place.Confidence != 1 {
		t.Errorf("belem = %+v", belem)
	}

	// The map's name adds the city, and the map knows the place as notable.
	market := got[2]
	if !market.Found || market.Place.OSMID != 30 || market.Place.Confidence != confidenceMapAddsOne {
		t.Errorf("market = %+v", market)
	}

	// A bakery is not what a plan means by a sight.
	if got[3].Found {
		t.Errorf("a shop answered for a sight: %+v", got[3])
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
	for _, want := range []string{"z[uùúûü]l[eèéêë]j[oòóôõö]", "b[eèéêë]l[eèéêë]m", "name:en", "name:es", "[bbox:38.61450,-9.27747,38.83010,-9.00113]", "[!highway]", "nwr[highway=pedestrian]"} {
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
	names := sights("Belém Tower", "Oceanário de Lisboa")
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
	if _, err := o.FindPlaces(context.Background(), sights("Belém Tower", "Museu do Fado"), lisbon); err != nil {
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
	names := make([]PlaceQuery, 0, maxPlacesPerQuery+5)
	for i := range maxPlacesPerQuery + 5 {
		names = append(names, PlaceQuery{Name: "Lugar " + strings.Repeat(string(rune('a'+i%26)), 4+i/26)})
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

	got, err := o.FindPlaces(context.Background(), sights("Belém Tower"), lisbon)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if len(got) != 1 || got[0].Found {
		t.Errorf("got = %+v, want one not-found answer", got)
	}

	// The map comes back: the name is asked for again, not served as a miss.
	f.status, f.body = 0, lisbonBody
	got, err = o.FindPlaces(context.Background(), sights("Belém Tower"), lisbon)
	if err != nil || !got[0].Found {
		t.Errorf("after recovery: %+v, %v", got, err)
	}
}

func TestFindPlacesRespectsTheBudget(t *testing.T) {
	f := &fakeMap{body: lisbonBody}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	o := NewOverpass(OverpassConfig{BaseURL: srv.URL}, testClient(), testCache(t), routing.NewDailyBudget(1))

	if _, err := o.FindPlaces(context.Background(), sights("Belém Tower"), lisbon); err != nil {
		t.Fatal(err)
	}
	if _, err := o.FindPlaces(context.Background(), sights("Museu do Fado"), lisbon); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable once the budget is spent", err)
	}
	if f.hits.Load() != 1 {
		t.Errorf("asked the map %d times, want 1", f.hits.Load())
	}
}

func TestFindPlacesQueryCannotBeBrokenOutOf(t *testing.T) {
	f, o := newMap(t, `{"elements":[]}`)
	_, _ = o.FindPlaces(context.Background(), sights(`Belém"];node(1);out;//`), lisbon)
	if len(f.queries) != 1 {
		t.Fatalf("queries = %d", len(f.queries))
	}
	if q := f.queries[0]; strings.Contains(q, "node(1)") || strings.Contains(q, "//") || strings.Count(q, "\n") != 5 {
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
		// A word saying what kind of place it is names something else.
		{"Las Setas", "Farmacia Las Setas", 0},
		{"Metropol Parasol", "Parque Metropol Parasol", 0},
		{"Catedral de Barcelona", "La Botiga de la Catedral de Barcelona", 0},
		{"Real Alcázar", "Real Alcázar de Sevilla", confidenceMapAddsOne},
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

// Each case is cut from what the map server answered for a real plan, and
// each one was matched wrongly, or not at all, by a simpler rule.
func TestMatchPlace(t *testing.T) {
	type el = element
	node := func(id int64, lat, lon float64, tags map[string]string) el {
		return el{Type: "node", ID: id, Lat: lat, Lon: lon, Tags: tags}
	}
	way := func(id int64, lat, lon float64, tags map[string]string) el {
		return el{Type: "way", ID: id, Center: &struct{ Lat, Lon float64 }{lat, lon}, Tags: tags}
	}
	tests := []struct {
		name   string
		ask    PlaceQuery
		els    []el
		wantID int64 // 0: no answer
	}{
		{
			name: "a bar that took a museum's name is not the museum",
			ask:  PlaceQuery{Name: "MACBA"},
			els: []el{
				node(1, 41.3828, 2.1665, map[string]string{"amenity": "bar", "name": "MACBA"}),
				node(2, 41.3831, 2.1667, map[string]string{"tourism": "museum", "name": "Museu d'Art Contemporani de Barcelona", "short_name": "MACBA", "wikidata": "Q1067140"}),
			},
			wantID: 2,
		},
		{
			name: "with no sight of that name, a café is not an answer for a sight",
			ask:  PlaceQuery{Name: "Puerta del Sol"},
			els: []el{
				node(1, 40.4151, -3.7032, map[string]string{"amenity": "cafe", "name": "Puerta del Sol"}),
			},
		},
		{
			name: "the same café is an answer for a plan that means a café",
			ask:  PlaceQuery{Name: "Puerta del Sol", Venue: true},
			els: []el{
				node(1, 40.4151, -3.7032, map[string]string{"amenity": "cafe", "name": "Puerta del Sol"}),
			},
			wantID: 1,
		},
		{
			name: "a café the map knows as notable answers for any plan",
			ask:  PlaceQuery{Name: "A Brasileira"},
			els: []el{
				node(1, 38.7107, -9.1420, map[string]string{"amenity": "cafe", "name": "A Brasileira", "wikidata": "Q2547006"}),
			},
			wantID: 1,
		},
		{
			name: "of a chain's branches, the one the map knows as notable, though another carries the bare name",
			ask:  PlaceQuery{Name: "Livraria Bertrand", Venue: true},
			els: []el{
				node(1, 38.6811, -9.1547, map[string]string{"shop": "books", "name": "Livraria Bertrand"}),
				node(2, 38.7107, -9.1412, map[string]string{"shop": "books", "name": "Livraria Bertrand - Chiado", "wikidata": "Q1866547"}),
			},
			wantID: 2,
		},
		{
			name: "branches the map cannot tell apart are no answer",
			ask:  PlaceQuery{Name: "A Padaria Portuguesa", Venue: true},
			els: []el{
				node(1, 38.7100, -9.1400, map[string]string{"shop": "bakery", "name": "A Padaria Portuguesa"}),
				node(2, 38.7400, -9.1500, map[string]string{"shop": "bakery", "name": "A Padaria Portuguesa"}),
			},
		},
		{
			name: "another business whose name adds a word is not the one asked for",
			ask:  PlaceQuery{Name: "Café Iruña", Venue: true},
			els: []el{
				node(1, 43.2300, -2.9800, map[string]string{"amenity": "restaurant", "name": "Cafe Iruña Berria"}),
			},
		},
		{
			name: "a square drawn as a pedestrian area, among namesakes in other towns",
			ask:  PlaceQuery{Name: "Plaza de España"},
			els: []el{
				way(1, 37.3766, -5.9863, map[string]string{"highway": "pedestrian", "area": "yes", "tourism": "attraction", "name": "Plaza de España", "wikidata": "Q956018"}),
				way(2, 37.3778, -5.9871, map[string]string{"highway": "pedestrian", "area": "yes", "tourism": "attraction", "name": "Plaza de España", "wikidata": "Q956018"}),
				way(3, 37.4618, -6.0137, map[string]string{"highway": "pedestrian", "area": "yes", "name": "Plaza de España"}),
				way(4, 37.4186, -6.1112, map[string]string{"highway": "residential", "name": "Plaza de España"}),
			},
			wantID: 1,
		},
		{
			name: "two notable places with one name are no answer",
			ask:  PlaceQuery{Name: "Capela das Almas"},
			els: []el{
				node(1, 41.1498, -8.6055, map[string]string{"amenity": "place_of_worship", "name": "Capela das Almas", "wikidata": "Q9695712"}),
				node(2, 41.1495, -8.6513, map[string]string{"amenity": "place_of_worship", "name": "Capela de Santa Catarina", "alt_name": "Capela das Almas", "wikidata": "Q54566003"}),
			},
		},
		{
			name: "a board about the sight, a car park and a clinic named after it are not the sight",
			ask:  PlaceQuery{Name: "Sagrada Família", Venue: true},
			els: []el{
				node(1, 41.4029, 2.1722, map[string]string{"amenity": "parking", "name": "Sagrada Familia"}),
				node(2, 41.4040, 2.1745, map[string]string{"tourism": "information", "name": "Sagrada Família"}),
				node(3, 41.4102, 2.1773, map[string]string{"amenity": "clinic", "name": "Sagrada Família"}),
				node(4, 41.4044, 2.1749, map[string]string{"place": "quarter", "name": "la Sagrada Família", "wikidata": "Q2470217"}),
			},
		},
		{
			name: "a name in the plan's language where the map's own is in another",
			ask:  PlaceQuery{Name: "Mercado Central"},
			els: []el{
				node(1, 39.4736, -0.3790, map[string]string{"amenity": "marketplace", "name": "Mercat Central", "name:es": "Mercado Central"}),
			},
			wantID: 1,
		},
		{
			name: "a hotel that kept the old name of what stood there is not it",
			ask:  PlaceQuery{Name: "Estadio Santiago Bernabéu", Venue: true},
			els: []el{
				node(1, 40.4500, -3.6900, map[string]string{"tourism": "hotel", "name": "Canopy", "old_name": "Estadio Santiago Bernabéu"}),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := matchPlace(tt.ask, tt.els)
			if found != (tt.wantID != 0) || (found && got.OSMID != tt.wantID) {
				t.Errorf("matchPlace = %+v, %v; want id %d", got, found, tt.wantID)
			}
		})
	}
}

func TestFindPlacesDoesNotTrustAnAnswerThatWasCutOff(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"elements":[`)
	for i := range maxPlaceCandidates {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"type":"node","id":` + strconv.Itoa(i+1) + `,"lat":38.7,"lon":-9.1,"tags":{"tourism":"museum","name":"Museu do Fado"}}`)
	}
	b.WriteString(`]}`)
	_, o := newMap(t, b.String())
	got, err := o.FindPlaces(context.Background(), sights("Museu do Fado"), lisbon)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Found {
		t.Errorf("answered from a list the server cut short: %+v", got[0])
	}
}
