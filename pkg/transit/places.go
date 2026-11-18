package transit

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/FACorreiaa/loci-connect-api/pkg/placematch"
)

const (
	// samePlaceM is how far apart two map entries with the same name can be
	// and still be one place: a museum's node, its outline and the café
	// inside it. Further apart they are two places and the name alone cannot
	// say which was meant.
	samePlaceM = 150

	// maxPlacesPerQuery is how many names one query asks about; longer lists
	// are asked in several queries. Small on purpose: each query's answers
	// are kept as soon as it returns, so a caller that runs out of time has
	// still made progress, and fewer words in a query means fewer answers
	// too long to trust.
	maxPlacesPerQuery = 15
	// maxPlaceCandidates bounds one answer. The map server cuts a longer one
	// off with no say in what is left out, so an answer this long is not
	// trusted: the words are asked again in two halves.
	maxPlaceCandidates = 3000
	// placesHalfSideM is half the side of the square searched, centred on the
	// city. A square because the map server answers "inside this box" several
	// times faster than "within this distance".
	placesHalfSideM = 12000
)

// Confidence of a name match, by how the two names relate. Anything looser
// is not a match at all.
const (
	confidenceExact      = 1.0 // the same words
	confidenceMapAddsOne = 0.9 // the map adds one word ("Casa Fuster Barcelona")
)

// MapPlace is a named place found on the map.
type MapPlace struct {
	// OSMType is "node", "way" or "relation"; with OSMID it identifies the
	// map entry.
	OSMType string  `json:"osm_type"`
	OSMID   int64   `json:"osm_id"`
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	// Kind is what the map says the place is ("museum", "restaurant").
	Kind string `json:"kind,omitempty"`
	// OpeningHours is the map's opening_hours tag as written; often empty.
	OpeningHours string `json:"opening_hours,omitempty"`
	// Wikidata is the map's wikidata tag ("Q12345"); often empty.
	Wikidata string `json:"wikidata,omitempty"`
	// Confidence says how closely the names agree: 1 for the same words, 0.9
	// when the map's name adds one.
	Confidence float64 `json:"confidence"`
}

// PlaceQuery is one place to look for.
type PlaceQuery struct {
	Name string
	// Venue says the plan means somewhere to eat, drink, shop or sleep. Only
	// then is a restaurant, a shop or a hotel an answer: businesses borrow
	// the names of the sights beside them ("Farmacia Las Setas", a bar called
	// "Plaza Mayor"), and one of those must not stand in for the sight.
	Venue bool
}

// PlaceMatch is the answer for one asked name.
type PlaceMatch struct {
	Place MapPlace `json:"place"`
	Found bool     `json:"found"`
}

// placeNameKeys are the tags a place's name can be under. A traveller's plan
// often has the English name while the map's name tag has the local one.
//
// old_name is left out on purpose: hotels and shops keep the name of what
// stood there before, and that is not where the traveller is going.
//
// The name:xx tags matter where the map's own name is in another language
// than the plan's: Valencia's central market is "Mercat Central" on the map
// and "Mercado Central" in a plan, Bilbao's theatre is "Arriaga antzokia".
var placeNameKeys = []string{"name", "name:en", "name:es", "name:pt", "name:ca", "int_name", "official_name", "alt_name", "short_name", "loc_name"}

const placeNameKeyRegex = `^(name|name:en|name:es|name:pt|name:ca|int_name|official_name|alt_name|short_name|loc_name)$`

// placeClass says what kind of answer a map entry can be.
type placeClass int

const (
	// classNone is never an answer: a car park, a clinic, a school, a bus
	// stop, an information board about the sight rather than the sight.
	classNone placeClass = iota
	// classVenue is somewhere to eat, drink, shop or sleep. An answer only
	// for a plan that means one, or when the map knows it as notable.
	classVenue
	// classSight is somewhere a traveller goes to see.
	classSight
)

// sightTags and venueTags say which values of a tag make an entry a sight or
// a venue; a nil set means any value. They are lists of what counts, so a
// kind of thing nobody thought of is not an answer until it is added.
var sightTags = []struct {
	key    string
	values map[string]bool
}{
	{"tourism", map[string]bool{"attraction": true, "museum": true, "viewpoint": true, "artwork": true, "gallery": true, "zoo": true, "aquarium": true, "theme_park": true}},
	{"historic", nil},
	{"amenity", map[string]bool{"place_of_worship": true, "marketplace": true, "food_court": true, "theatre": true, "cinema": true, "arts_centre": true, "library": true, "fountain": true, "planetarium": true, "monastery": true, "townhall": true, "university": true, "exhibition_centre": true}},
	{"leisure", map[string]bool{"park": true, "garden": true, "stadium": true, "nature_reserve": true, "water_park": true}},
	{"man_made", nil},
	{"natural", nil},
	{"place", map[string]bool{"square": true}},
	// A square or a pedestrian street: the only kind of road that is a place.
	{"highway", map[string]bool{"pedestrian": true}},
}

var venueTags = []struct {
	key    string
	values map[string]bool
}{
	{"amenity", map[string]bool{"restaurant": true, "cafe": true, "bar": true, "pub": true, "fast_food": true, "biergarten": true, "ice_cream": true, "nightclub": true}},
	{"shop", nil},
	{"tourism", map[string]bool{"hotel": true, "hostel": true, "guest_house": true, "apartment": true, "motel": true}},
	{"craft", map[string]bool{"brewery": true, "winery": true, "distillery": true}},
}

// placeKindWords say what kind of place a name is, not which one, in the
// languages plans and maps here are written in. They are poor search words
// ("museum" finds nothing under a map name that says "museu"), and a map
// name that adds one names a different thing: the "Farmacia Las Setas" is not
// "Las Setas", and the "Parque Metropol Parasol" playground is not the
// Metropol Parasol.
var placeKindWords = map[string]bool{
	"museum": true, "museu": true, "museo": true, "gallery": true, "galeria": true,
	"church": true, "igreja": true, "iglesia": true, "esglesia": true, "cathedral": true, "catedral": true, "basilica": true,
	"chapel": true, "capela": true, "capilla": true, "monastery": true, "mosteiro": true, "monasterio": true, "monestir": true,
	"convent": true, "convento": true, "palace": true, "palacio": true, "palau": true, "castle": true, "castelo": true, "castillo": true, "castell": true,
	"tower": true, "torre": true, "bridge": true, "ponte": true, "puente": true, "pont": true,
	"park": true, "parque": true, "parc": true, "garden": true, "gardens": true, "jardim": true, "jardin": true, "jardines": true, "jardi": true,
	"square": true, "praca": true, "plaza": true, "placa": true, "largo": true, "street": true, "rua": true, "calle": true, "carrer": true, "avenida": true, "avinguda": true,
	"market": true, "mercado": true, "mercat": true, "restaurant": true, "restaurante": true, "cafe": true, "bar": true, "casa": true, "house": true,
	"viewpoint": true, "miradouro": true, "mirador": true, "beach": true, "praia": true, "playa": true, "platja": true,
	"station": true, "estacao": true, "estacion": true, "estacio": true, "metro": true,
	"hotel": true, "hostal": true, "hostel": true, "pension": true, "pensao": true, "apartments": true, "apartamentos": true,
	"pharmacy": true, "farmacia": true, "shop": true, "store": true, "loja": true, "tienda": true, "botiga": true,
	"parking": true, "aparcamiento": true, "estacionamento": true, "school": true, "escola": true, "escuela": true,
	"clinic": true, "clinica": true, "hospital": true, "centro": true, "centre": true, "center": true,
	"taberna": true, "tasca": true, "bodega": true, "cerveceria": true, "cervejaria": true, "pasteleria": true, "pastelaria": true,
	"tunnel": true, "tunel": true, "entrada": true, "entrance": true,
}

// genericPlaceWords are the words that do not say which place a name means:
// the kind words, and the small or common words around them.
var genericPlaceWords = func() map[string]bool {
	out := map[string]bool{
		"the": true, "and": true, "of": true, "dos": true, "das": true, "del": true, "los": true, "las": true,
		"national": true, "nacional": true, "royal": true, "real": true,
		"saint": true, "santa": true, "santo": true, "sao": true, "san": true, "sant": true, "old": true, "new": true, "city": true,
	}
	for w := range placeKindWords {
		out[w] = true
	}
	return out
}()

// placeWord is the longest word of a name that says which place it is. When
// every word is generic it falls back to the longest word at all, so
// "Mercado Central" is still looked for.
func placeWord(name string) string {
	best, fallback := "", ""
	for _, w := range strings.FieldsFunc(placematch.Fold(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) < 3 {
			continue
		}
		if len(w) > len(fallback) {
			fallback = w
		}
		if !genericPlaceWords[w] && len(w) > len(best) {
			best = w
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

func placeCacheKey(q PlaceQuery, near LatLon) string {
	kind := "sight"
	if q.Venue {
		kind = "venue"
	}
	return fmt.Sprintf("transit:overpass:v2:place:%.2f,%.2f:%s:%s", near.Lat, near.Lon, kind, placematch.Fold(strings.TrimSpace(q.Name)))
}

// FindPlaces looks for each place near a point (the city centre) and answers
// in the order asked. It answers Found=false rather than guess: the names
// must agree closely, the entry must be the kind of place that was meant, and
// a name that fits two places in different spots fits neither.
//
// All names not already cached are asked in as few queries as possible. When
// a query fails the names it covered come back not found alongside the
// error, and are not cached, so a caller can show what it has and try the
// rest later.
func (o *Overpass) FindPlaces(ctx context.Context, asks []PlaceQuery, near LatLon) ([]PlaceMatch, error) {
	out := make([]PlaceMatch, len(asks))
	type ask struct {
		idx  int
		word string
	}
	var pending []ask
	for i, q := range asks {
		word := placeWord(q.Name)
		if word == "" {
			continue
		}
		if o.cacheGet(placeCacheKey(q, near), &out[i]) {
			continue
		}
		pending = append(pending, ask{idx: i, word: word})
	}

	for start := 0; start < len(pending); start += maxPlacesPerQuery {
		batch := pending[start:min(start+maxPlacesPerQuery, len(pending))]
		words := make([]string, 0, len(batch))
		seen := map[string]bool{}
		for _, a := range batch {
			if !seen[a.word] {
				seen[a.word] = true
				words = append(words, a.word)
			}
		}
		els, err := o.placeCandidates(ctx, words, near)
		if err != nil {
			return out, err
		}
		for _, a := range batch {
			place, found := matchPlace(asks[a.idx], els)
			out[a.idx] = PlaceMatch{Place: place, Found: found}
			ttl := ttlPlaceMiss
			if found {
				ttl = ttlPlaceHit
			}
			o.cacheSet(placeCacheKey(asks[a.idx], near), out[a.idx], ttl)
		}
	}
	return out, nil
}

// placeCandidates fetches every map entry near the point whose name holds
// one of the words. An answer long enough to have been cut off is not used:
// the words are split and asked again. A single word whose answer is still
// cut off brings nothing back, so the names behind it go unanswered: with
// entries missing, the one left standing could be the wrong one of two.
func (o *Overpass) placeCandidates(ctx context.Context, words []string, near LatLon) ([]element, error) {
	els, err := o.run(ctx, placesQuery(words, near))
	if err != nil {
		return nil, err
	}
	if len(els) < maxPlaceCandidates {
		return els, nil
	}
	if len(words) < 2 {
		return nil, nil
	}
	half := len(words) / 2
	first, err := o.placeCandidates(ctx, words[:half], near)
	if err != nil {
		return nil, err
	}
	second, err := o.placeCandidates(ctx, words[half:], near)
	if err != nil {
		return nil, err
	}
	return append(first, second...), nil
}

// placesQuery asks for every entry in a square around a point whose name,
// under any of the name tags, holds one of the words as a whole word.
//
// The shape is what the map server can answer in time, measured against the
// one production uses. Every word goes into one pattern, so the area's names
// are read once: asking word by word reads them once per word, and a plan's
// worth of words then runs out the clock (3 s a word, 30 words). Roads, bus
// stops, rail and routes are left out at the server, since they are never a
// place to visit and are most of what a common word finds. Pedestrian ways
// are asked for apart: that is how the map draws a square.
//
// The words hold only letters and digits (see looseRegex), so nothing in
// them can end the pattern or the statement.
func placesQuery(words []string, near LatLon) string {
	alts := make([]string, len(words))
	for i, w := range words {
		alts[i] = looseRegex(w)
	}
	dLat := placesHalfSideM / 111_320.0
	dLon := dLat / math.Max(0.1, math.Cos(near.Lat*math.Pi/180))
	name := fmt.Sprintf(`[~"%s"~"(^|[^[:alpha:]])(%s)([^[:alpha:]]|$)",i]`, placeNameKeyRegex, strings.Join(alts, "|"))
	return fmt.Sprintf(`[out:json][timeout:%d][bbox:%.5f,%.5f,%.5f,%.5f];
(
  nwr%s[!highway][!public_transport][!railway][!route][!boundary];
  nwr[highway=pedestrian]%s;
);
out tags center %d;`,
		queryTimeoutS, near.Lat-dLat, near.Lon-dLon, near.Lat+dLat, near.Lon+dLon,
		name, name, maxPlaceCandidates)
}

// classify says what kind of answer a map entry can be, and what the map
// calls it ("museum", "restaurant"). An entry with a sight's tag is a sight
// whatever else it carries: the café that is itself an attraction is one.
func classify(tags map[string]string) (placeClass, string) {
	for _, k := range []string{"public_transport", "railway", "route", "boundary"} {
		if tags[k] != "" {
			return classNone, ""
		}
	}
	if hw := tags["highway"]; hw != "" && hw != "pedestrian" {
		return classNone, ""
	}
	for _, t := range sightTags {
		v := strings.TrimSpace(tags[t.key])
		if v == "" || v == "no" || (t.values != nil && !t.values[v]) {
			continue
		}
		if t.key == "highway" {
			v = "square"
		}
		return classSight, clip(v, 40)
	}
	for _, t := range venueTags {
		v := strings.TrimSpace(tags[t.key])
		if v == "" || v == "no" || (t.values != nil && !t.values[v]) {
			continue
		}
		return classVenue, clip(v, 40)
	}
	// A building the map says nothing more about is a sight only when the
	// map also knows it as notable; otherwise it is an address.
	if b := tags["building"]; b != "" && b != "no" && tags["wikidata"] != "" {
		return classSight, "building"
	}
	return classNone, ""
}

// nameConfidence scores how closely a map name agrees with the asked one; 0
// is no match. A map name that leaves out a word of the asked one is never a
// match: "National Museum" is not the "National Tile Museum". Nor is one
// that adds a word saying what kind of place it is, which names something
// else beside the place asked for.
func nameConfidence(given, candidate string) float64 {
	ok, extra := placematch.NamedBy(given, candidate)
	switch {
	case !ok:
		return 0
	case extra == 0:
		return confidenceExact
	}
	asked := placematch.Tokens(given)
	for w := range placematch.Tokens(candidate) {
		if !asked[w] && placeKindWords[w] {
			return 0
		}
	}
	return confidenceMapAddsOne
}

type placeCandidate struct {
	MapPlace
	class placeClass
}

// matchPlace picks the map entry a plan means, if exactly one place fits.
//
// Every step drops candidates and none of them guesses:
//  1. where a sight has the closest name, it is the answer and a business
//     with the same name is not;
//  2. otherwise the businesses are weighed, exact names and names with a
//     word added alike: a chain's branches share a name and the map tells
//     them apart by adding one ("Livraria Bertrand - Chiado"), so the one
//     branch that happens to carry the bare name is not thereby the one
//     meant;
//  3. a business is an answer only for a plan that means one, or when the
//     map knows it as notable;
//  4. a name with a word added is an answer only when the map knows the
//     place as notable. "Real Alcázar de Sevilla" is the Real Alcázar; the
//     "Cafe Iruña Berria" across town is not the Café Iruña, and nothing in
//     the two names says which case this is;
//  5. what is left must be one place. Entries count as one place when they
//     are within samePlaceM of each other or carry the same wikidata id (a
//     park's outline and its gate). Of several places, one the map knows as
//     notable wins over ones it does not; otherwise the name fits no one
//     place and there is no answer.
func matchPlace(q PlaceQuery, els []element) (MapPlace, bool) {
	var cands []placeCandidate
	top := 0.0
	for _, e := range els {
		pt, ok := e.point()
		class, kind := classify(e.Tags)
		if !ok || class == classNone {
			continue
		}
		conf, matched := 0.0, ""
		for _, k := range placeNameKeys {
			for _, cand := range strings.Split(e.Tags[k], ";") {
				cand = strings.TrimSpace(cand)
				if c := nameConfidence(q.Name, cand); c > conf {
					conf, matched = c, cand
				}
			}
		}
		if conf == 0 {
			continue
		}
		top = math.Max(top, conf)
		name := strings.TrimSpace(e.Tags["name"])
		if name == "" {
			name = matched
		}
		cands = append(cands, placeCandidate{class: class, MapPlace: MapPlace{
			OSMType: e.Type, OSMID: e.ID, Name: clip(name, 200), Lat: pt.Lat, Lon: pt.Lon, Kind: kind,
			OpeningHours: clip(strings.TrimSpace(e.Tags["opening_hours"]), 500),
			Wikidata:     clip(strings.TrimSpace(e.Tags["wikidata"]), 20),
			Confidence:   conf,
		}})
	}

	sights := keepPlaces(cands, func(c placeCandidate) bool { return c.class == classSight && c.Confidence == top })
	if len(sights) > 0 {
		cands = sights
	} else {
		cands = keepPlaces(cands, func(c placeCandidate) bool {
			return c.class == classVenue && (q.Venue || c.Wikidata != "")
		})
	}
	if len(cands) == 0 {
		return MapPlace{}, false
	}

	places := groupPlaces(cands)
	if len(places) > 1 {
		places = keepGroups(places, func(g []placeCandidate) bool {
			for _, c := range g {
				if c.Wikidata != "" {
					return true
				}
			}
			return false
		})
	}
	if len(places) != 1 {
		return MapPlace{}, false
	}
	best := bestEntry(places[0])
	if best.Confidence < confidenceExact && best.Wikidata == "" {
		return MapPlace{}, false
	}
	return best, true
}

// keepPlaces is the candidates that pass.
func keepPlaces(cands []placeCandidate, pass func(placeCandidate) bool) []placeCandidate {
	var kept []placeCandidate
	for _, c := range cands {
		if pass(c) {
			kept = append(kept, c)
		}
	}
	return kept
}

func keepGroups(groups [][]placeCandidate, pass func([]placeCandidate) bool) [][]placeCandidate {
	var kept [][]placeCandidate
	for _, g := range groups {
		if pass(g) {
			kept = append(kept, g)
		}
	}
	return kept
}

// groupPlaces gathers the entries that are one place: within samePlaceM of
// an entry already in the group, or sharing its wikidata id.
func groupPlaces(cands []placeCandidate) [][]placeCandidate {
	var groups [][]placeCandidate
next:
	for _, c := range cands {
		for i, g := range groups {
			for _, m := range g {
				same := c.Wikidata != "" && c.Wikidata == m.Wikidata
				if same || DistanceM(LatLon{Lat: m.Lat, Lon: m.Lon}, LatLon{Lat: c.Lat, Lon: c.Lon}) <= samePlaceM {
					groups[i] = append(groups[i], c)
					continue next
				}
			}
		}
		groups = append(groups, []placeCandidate{c})
	}
	return groups
}

// bestEntry picks, of the entries that are one place, the one that says most:
// the one the map knows as notable, then the one with opening hours.
func bestEntry(group []placeCandidate) MapPlace {
	score := func(c placeCandidate) int {
		n := 0
		if c.Wikidata != "" {
			n += 4
		}
		if c.Confidence == confidenceExact {
			n += 2
		}
		if c.OpeningHours != "" {
			n++
		}
		return n
	}
	best := group[0]
	for _, c := range group[1:] {
		if score(c) > score(best) {
			best = c
		}
	}
	return best.MapPlace
}
