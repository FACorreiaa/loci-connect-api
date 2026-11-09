package flights

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
	"github.com/FACorreiaa/loci-connect-api/pkg/placematch"
)

//go:embed airports.csv
var airportsCSV string

// minQueryLen is the shortest autocomplete query worth answering: one letter
// matches half the world.
const minQueryLen = 2

// nearKm is how far from the page's coordinates an airport (or a metro's
// centre) may be and still serve that city. Wide enough for airports that sit
// out of town (Milan Malpensa, ~45 km), narrow enough that Birmingham UK can
// never land in Alabama.
const nearKm = 150.0

// PlaceKind says whether a PlaceOption is one airport or a metro code.
type PlaceKind int

const (
	KindAirport PlaceKind = 1
	KindMetro   PlaceKind = 2
)

// PlaceOption is an airport or metro code a traveller can fly from or to.
type PlaceOption struct {
	IATA        string
	Name        string
	City        string
	CountryCode string
	Kind        PlaceKind
	Lat, Lon    float64
}

type airport struct {
	PlaceOption
	size     int    // 2 large, 1 medium
	rawCity  string // folded municipality as written ("dubai(jebel ali)")
	cityKey  string // folded municipality, qualifier dropped ("dubai")
	nameOnly string // folded airport name
	nameKey  string // folded name and keywords
	countryU string
}

// Match strengths for a city name against an airport, strongest first.
const (
	matchKeyword  = 1 // only the keywords mention it
	matchName     = 2 // the airport's name carries it ("Venice Marco Polo")
	matchCity     = 3 // the municipality, once its qualifier is dropped
	matchCityFull = 4 // the municipality exactly as written
)

// Airports is the embedded airport dataset (OurAirports, public domain;
// regenerate with go run ./pkg/flights/cmd/gen-airports). Safe for
// concurrent use: it is read-only after LoadAirports.
type Airports struct {
	all    []airport
	metros []PlaceOption
	// metroKeys maps a folded city name or alias to its metros index.
	metroKeys map[string]int
}

// LoadAirports parses the embedded dataset.
func LoadAirports() (*Airports, error) {
	r := csv.NewReader(strings.NewReader(airportsCSV))
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("airports.csv: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("airports.csv: no rows")
	}
	a := &Airports{metroKeys: map[string]int{}}
	for _, rec := range rows[1:] {
		if len(rec) < 8 {
			continue
		}
		lat, err1 := strconv.ParseFloat(rec[4], 64)
		lon, err2 := strconv.ParseFloat(rec[5], 64)
		size, err3 := strconv.Atoi(rec[6])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		a.all = append(a.all, airport{
			PlaceOption: PlaceOption{IATA: rec[0], Name: rec[1], City: rec[2], CountryCode: rec[3], Kind: KindAirport, Lat: lat, Lon: lon},
			size:        size,
			rawCity:     normalize(rec[2]),
			cityKey:     cityKey(rec[2]),
			nameOnly:    normalize(rec[1]),
			nameKey:     normalize(rec[1] + " " + rec[7]),
			countryU:    strings.ToUpper(rec[3]),
		})
	}
	for i, m := range metros {
		a.metros = append(a.metros, PlaceOption{
			IATA: m.IATA, Name: m.City + " (all airports)", City: m.City, CountryCode: m.Country, Kind: KindMetro,
			Lat: m.Lat, Lon: m.Lon,
		})
		a.metroKeys[normalize(m.City)] = i
		for _, al := range m.Aliases {
			a.metroKeys[normalize(al)] = i
		}
	}
	return a, nil
}

// normalize folds case and accents and collapses whitespace.
func normalize(s string) string {
	return strings.Join(strings.Fields(placematch.Fold(s)), " ")
}

// cityKey drops a qualifier: "San José (Alajuela)", "Birmingham, West
// Midlands" and "Dubai(Jebel Ali)" are San José, Birmingham and Dubai.
func cityKey(s string) string {
	if i := strings.IndexAny(s, "(,"); i > 0 {
		s = s[:i]
	}
	return normalize(s)
}

// Search is the autocomplete: an exact IATA code first, then metros and
// cities starting with the query, then airports with a word in their name
// starting with it. Larger airports come first within each rank.
func (a *Airports) Search(query string, limit int) []PlaceOption {
	q := normalize(query)
	if len([]rune(q)) < minQueryLen {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}
	upper := strings.ToUpper(q)

	type hit struct {
		opt  PlaceOption
		rank int
		size int
	}
	var hits []hit
	for i, m := range a.metros {
		switch {
		case m.IATA == upper:
			hits = append(hits, hit{m, 0, 3})
		case metroMatches(metros[i], q):
			hits = append(hits, hit{m, 1, 3})
		}
	}
	for _, ap := range a.all {
		switch {
		case ap.IATA == upper:
			hits = append(hits, hit{ap.PlaceOption, 0, ap.size})
		case ap.cityKey == q:
			hits = append(hits, hit{ap.PlaceOption, 2, ap.size})
		case strings.HasPrefix(ap.cityKey, q):
			hits = append(hits, hit{ap.PlaceOption, 3, ap.size})
		case wordPrefix(ap.nameKey, q):
			hits = append(hits, hit{ap.PlaceOption, 4, ap.size})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		if hits[i].size != hits[j].size {
			return hits[i].size > hits[j].size
		}
		return hits[i].opt.IATA < hits[j].opt.IATA
	})
	out := make([]PlaceOption, 0, min(limit, len(hits)))
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		out = append(out, h.opt)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func metroMatches(m metro, q string) bool {
	if strings.HasPrefix(normalize(m.City), q) {
		return true
	}
	for _, al := range m.Aliases {
		if strings.HasPrefix(normalize(al), q) {
			return true
		}
	}
	return false
}

// wordPrefix reports whether any word of s, or s itself, starts with q.
func wordPrefix(s, q string) bool {
	if strings.HasPrefix(s, q) {
		return true
	}
	return strings.Contains(s, " "+q) || strings.Contains(s, "-"+q) || strings.Contains(s, "/"+q)
}

// Resolve picks the airport a city's flight search should use.
//
// With coordinates (what the clients send), only airports within nearKm
// count, so position decides between same-name cities; a metro code is
// offered only when the page is near its centre. Without them, countryCode
// narrows; with neither, a name found in more than one country is no match,
// never a guess. Among the airports left, the largest wins, then the one
// that is most plainly the city's own (municipality over airport name over
// keywords), then the nearest.
func (a *Airports) Resolve(city, countryCode string, lat, lon *float64) (PlaceOption, bool) {
	key := cityKey(city)
	if key == "" {
		return PlaceOption{}, false
	}
	cc := strings.ToUpper(strings.TrimSpace(countryCode))
	hasGeo := lat != nil && lon != nil
	if i, ok := a.metroKeys[key]; ok {
		m := a.metros[i]
		countryOK := cc == "" || m.CountryCode == cc
		nearOK := !hasGeo || geo.HaversineKm(*lat, *lon, m.Lat, m.Lon) <= nearKm
		if countryOK && nearOK {
			return m, true
		}
	}
	cands := a.cityAirports(key, cc)
	if hasGeo {
		near := cands[:0:0]
		for _, c := range cands {
			if geo.HaversineKm(*lat, *lon, c.Lat, c.Lon) <= nearKm {
				near = append(near, c)
			}
		}
		cands = near
	}
	cands = strongestTier(cands)
	if len(cands) == 0 || (!hasGeo && cc == "" && manyCountries(cands)) {
		return PlaceOption{}, false
	}
	return pickAirport(cands, lat, lon).PlaceOption, true
}

// ResolveName is Resolve for a bare name with no country or position: it
// answers only when the name is unambiguous (a metro, or airports all in one
// country), so "Valencia" alone resolves to nothing rather than to the wrong
// continent.
func (a *Airports) ResolveName(name string) (string, bool) {
	key := cityKey(name)
	if key == "" {
		return "", false
	}
	if i, ok := a.metroKeys[key]; ok {
		return a.metros[i].IATA, true
	}
	o, ok := a.Resolve(name, "", nil, nil)
	return o.IATA, ok
}

type candidate struct {
	airport
	match int
}

// cityAirports is every airport that serves the city by any evidence:
// municipality (with or without its qualifier), airport name, or keywords.
// The strength of the evidence travels with each, for pickAirport.
func (a *Airports) cityAirports(key, cc string) []candidate {
	var out []candidate
	for _, ap := range a.all {
		if cc != "" && ap.countryU != cc {
			continue
		}
		m := 0
		switch {
		case ap.rawCity == key:
			m = matchCityFull
		case ap.cityKey == key:
			m = matchCity
		case containsWord(ap.nameOnly, key):
			m = matchName
		case containsWord(ap.nameKey, key):
			m = matchKeyword
		}
		if m > 0 {
			out = append(out, candidate{ap, m})
		}
	}
	return out
}

// strongestTier keeps the candidates with the best kind of evidence: the
// city's own municipality when any airport has it, else airports named
// after the city, else keyword mentions. "Porto" is OPO's municipality, so
// Porto Alegre's name does not make it ambiguous; "Venice" is no airport's
// municipality (VCE's is Venezia), so its name decides.
func strongestTier(cs []candidate) []candidate {
	tier := func(m int) int {
		if m >= matchCity {
			return 2
		}
		if m == matchName {
			return 1
		}
		return 0
	}
	best := -1
	for _, c := range cs {
		best = max(best, tier(c.match))
	}
	out := cs[:0:0]
	for _, c := range cs {
		if tier(c.match) == best {
			out = append(out, c)
		}
	}
	return out
}

func manyCountries(cs []candidate) bool {
	if len(cs) < 2 {
		return false
	}
	for _, c := range cs[1:] {
		if c.countryU != cs[0].countryU {
			return true
		}
	}
	return false
}

// containsWord reports whether key appears in s as whole words.
func containsWord(s, key string) bool {
	for _, sep := range []string{" ", "-", "/", ",", "(", ")"} {
		s = strings.ReplaceAll(s, sep, " ")
	}
	return strings.Contains(" "+strings.Join(strings.Fields(s), " ")+" ", " "+key+" ")
}

// pickAirport ranks by size, then match strength, then distance, then code
// (so the answer never depends on the dataset's row order).
func pickAirport(cands []candidate, lat, lon *float64) candidate {
	dist := func(c candidate) float64 {
		if lat == nil || lon == nil {
			return 0
		}
		return geo.HaversineKm(*lat, *lon, c.Lat, c.Lon)
	}
	best := cands[0]
	for _, c := range cands[1:] {
		switch {
		case c.size != best.size:
			if c.size > best.size {
				best = c
			}
		case c.match != best.match:
			if c.match > best.match {
				best = c
			}
		case dist(c) != dist(best):
			if dist(c) < dist(best) {
				best = c
			}
		case c.IATA < best.IATA:
			best = c
		}
	}
	return best
}
