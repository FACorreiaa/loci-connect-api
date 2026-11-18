package transit

import (
	"context"
	"fmt"
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

	// maxPlacesPerQuery keeps one query small enough to answer inside
	// queryTimeoutS; longer lists are asked in several queries.
	maxPlacesPerQuery = 25
	// placesPerName bounds the answer: a common word matches many entries.
	placesPerName = 40
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

// PlaceMatch is the answer for one asked name.
type PlaceMatch struct {
	Place MapPlace `json:"place"`
	Found bool     `json:"found"`
}

// placeNameKeys are the tags a place's name can be under. A traveller's plan
// often has the English name while the map's name tag has the local one.
var placeNameKeys = []string{"name", "name:en", "int_name", "official_name", "alt_name"}

const placeNameKeyRegex = `^(name|name:en|int_name|official_name|alt_name)$`

// placeKindKeys are the tags that say an entry is a place someone can visit,
// most telling first.
var placeKindKeys = []string{"tourism", "historic", "amenity", "shop", "leisure", "man_made", "natural", "place", "building"}

// genericPlaceWords say what kind of place a name is, not which one, in the
// languages plans and maps here are written in. They are poor search words:
// "museum" finds nothing under a map name that says "museu".
var genericPlaceWords = map[string]bool{
	"the": true, "and": true, "of": true, "dos": true, "das": true, "del": true, "los": true, "las": true,
	"museum": true, "museu": true, "museo": true, "gallery": true, "galeria": true,
	"church": true, "igreja": true, "iglesia": true, "cathedral": true, "catedral": true, "basilica": true,
	"chapel": true, "capela": true, "capilla": true, "monastery": true, "mosteiro": true, "monasterio": true,
	"convent": true, "convento": true, "palace": true, "palacio": true, "castle": true, "castelo": true, "castillo": true,
	"tower": true, "torre": true, "bridge": true, "ponte": true, "puente": true,
	"park": true, "parque": true, "garden": true, "gardens": true, "jardim": true, "jardin": true, "jardines": true,
	"square": true, "praca": true, "plaza": true, "placa": true, "street": true, "rua": true, "calle": true, "avenida": true,
	"market": true, "mercado": true, "mercat": true, "restaurant": true, "restaurante": true, "cafe": true, "bar": true, "casa": true, "house": true,
	"viewpoint": true, "miradouro": true, "mirador": true, "beach": true, "praia": true, "playa": true,
	"station": true, "estacao": true, "estacion": true, "national": true, "nacional": true, "royal": true, "real": true,
	"saint": true, "santa": true, "santo": true, "sao": true, "san": true, "old": true, "new": true, "city": true,
}

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

func placeCacheKey(name string, near LatLon) string {
	return fmt.Sprintf("transit:overpass:v1:place:%.2f,%.2f:%s", near.Lat, near.Lon, placematch.Fold(strings.TrimSpace(name)))
}

// FindPlaces looks for each named place near a point (the city centre) and
// answers in the order asked. It answers Found=false rather than guess: the
// names must agree closely, and a name that fits two places in different
// spots fits neither.
//
// All names not already cached are asked in as few queries as possible. When
// a query fails the names it covered come back not found alongside the
// error, and are not cached, so a caller can show what it has and try the
// rest later.
func (o *Overpass) FindPlaces(ctx context.Context, names []string, near LatLon) ([]PlaceMatch, error) {
	out := make([]PlaceMatch, len(names))
	type ask struct {
		idx  int
		word string
	}
	var pending []ask
	for i, name := range names {
		word := placeWord(name)
		if word == "" {
			continue
		}
		if o.cacheGet(placeCacheKey(name, near), &out[i]) {
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
		els, err := o.run(ctx, placesQuery(words, near))
		if err != nil {
			return out, err
		}
		for _, a := range batch {
			place, found := matchPlace(names[a.idx], els)
			out[a.idx] = PlaceMatch{Place: place, Found: found}
			ttl := ttlPlaceMiss
			if found {
				ttl = ttlPlaceHit
			}
			o.cacheSet(placeCacheKey(names[a.idx], near), out[a.idx], ttl)
		}
	}
	return out, nil
}

// placesQuery asks for every entry near a point whose name, under any of the
// name tags, holds one of the words. The words hold only letters and digits
// (placeWord), so nothing in them can end the quoted regex.
func placesQuery(words []string, near LatLon) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[out:json][timeout:%d];\n(\n", queryTimeoutS)
	for _, w := range words {
		fmt.Fprintf(&b, "  nwr(around:%d,%.5f,%.5f)[~\"%s\"~\"%s\",i];\n", lodgingRadiusM, near.Lat, near.Lon, placeNameKeyRegex, looseRegex(w))
	}
	fmt.Fprintf(&b, ");\nout tags center %d;", placesPerName*len(words))
	return b.String()
}

// placeKind is what the map says an entry is, or "" when it is not a place
// to visit: a bus stop, a street or a bus line named after the museum beside
// it must not compete with the museum.
func placeKind(tags map[string]string) string {
	for _, k := range []string{"highway", "public_transport", "railway", "route", "boundary"} {
		if tags[k] != "" {
			return ""
		}
	}
	for _, k := range placeKindKeys {
		if v := strings.TrimSpace(tags[k]); v != "" && v != "no" {
			if k == "building" {
				return "building"
			}
			return clip(v, 40)
		}
	}
	return ""
}

// nameConfidence scores how closely a map name agrees with the asked one; 0
// is no match. A map name that leaves out a word of the asked one is never a
// match: "National Museum" is not the "National Tile Museum".
func nameConfidence(given, candidate string) float64 {
	ok, extra := placematch.NamedBy(given, candidate)
	switch {
	case !ok:
		return 0
	case extra == 0:
		return confidenceExact
	}
	return confidenceMapAddsOne
}

// matchPlace picks the entry a name means, if exactly one place fits best.
func matchPlace(given string, els []element) (MapPlace, bool) {
	var best MapPlace
	found, tied := false, false
	for _, e := range els {
		pt, ok := e.point()
		kind := placeKind(e.Tags)
		if !ok || kind == "" {
			continue
		}
		conf, matched := 0.0, ""
		for _, k := range placeNameKeys {
			for _, cand := range strings.Split(e.Tags[k], ";") {
				cand = strings.TrimSpace(cand)
				if c := nameConfidence(given, cand); c > conf {
					conf, matched = c, cand
				}
			}
		}
		if conf == 0 {
			continue
		}
		name := strings.TrimSpace(e.Tags["name"])
		if name == "" {
			name = matched
		}
		p := MapPlace{
			OSMType: e.Type, OSMID: e.ID, Name: clip(name, 200), Lat: pt.Lat, Lon: pt.Lon, Kind: kind,
			OpeningHours: clip(strings.TrimSpace(e.Tags["opening_hours"]), 500),
			Wikidata:     clip(strings.TrimSpace(e.Tags["wikidata"]), 20),
			Confidence:   conf,
		}
		switch {
		case !found || conf > best.Confidence:
			best, found, tied = p, true, false
		case conf < best.Confidence:
		case DistanceM(LatLon{best.Lat, best.Lon}, pt) > samePlaceM:
			// Two places the name fits equally, in different spots.
			tied = true
		case best.OpeningHours == "" && p.OpeningHours != "":
			// The same place mapped twice: keep the entry that has hours.
			best = p
		}
	}
	if tied {
		return MapPlace{}, false
	}
	return best, found
}
