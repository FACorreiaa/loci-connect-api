package flights

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"math"
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
	cityKey  string // folded city, parenthetical dropped
	nameKey  string // folded name and keywords
	countryU string
}

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
			cityKey:     cityKey(rec[2]),
			nameKey:     normalize(rec[1] + " " + rec[7]),
			countryU:    strings.ToUpper(rec[3]),
		})
	}
	for i, m := range metros {
		a.metros = append(a.metros, PlaceOption{
			IATA: m.IATA, Name: m.City + " (all airports)", City: m.City, CountryCode: m.Country, Kind: KindMetro,
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

// cityKey drops a parenthetical ("San José (Alajuela)" is San José).
func cityKey(s string) string {
	if i := strings.Index(s, "("); i > 0 {
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
		case strings.HasPrefix(ap.cityKey, q):
			hits = append(hits, hit{ap.PlaceOption, 2, ap.size})
		case wordPrefix(ap.nameKey, q):
			hits = append(hits, hit{ap.PlaceOption, 3, ap.size})
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

// Resolve picks the airport a city's flight search should use: its metro code
// when it has one (in that country), else the largest airport serving the
// city, nearest to lat/lon when given. countryCode narrows same-name cities.
func (a *Airports) Resolve(city, countryCode string, lat, lon *float64) (PlaceOption, bool) {
	key := cityKey(city)
	if key == "" {
		return PlaceOption{}, false
	}
	cc := strings.ToUpper(strings.TrimSpace(countryCode))
	if i, ok := a.metroKeys[key]; ok && (cc == "" || a.metros[i].CountryCode == cc) {
		return a.metros[i], true
	}
	cands := a.cityAirports(key, cc)
	if len(cands) == 0 {
		return PlaceOption{}, false
	}
	return pickAirport(cands, lat, lon).PlaceOption, true
}

// ResolveName is Resolve for a bare name with no country: it answers only
// when the name is unambiguous (a metro, or airports all in one country), so
// "Valencia" alone resolves to nothing rather than to the wrong continent.
func (a *Airports) ResolveName(name string) (string, bool) {
	key := cityKey(name)
	if key == "" {
		return "", false
	}
	if i, ok := a.metroKeys[key]; ok {
		return a.metros[i].IATA, true
	}
	cands := a.cityAirports(key, "")
	if len(cands) == 0 {
		return "", false
	}
	for _, c := range cands[1:] {
		if c.countryU != cands[0].countryU {
			return "", false
		}
	}
	return pickAirport(cands, nil, nil).IATA, true
}

// cityAirports is every airport serving the city: its municipality, or, when
// none has that municipality, airports whose name carries the city ("Josep
// Tarradellas Barcelona-El Prat").
func (a *Airports) cityAirports(key, cc string) []airport {
	var byCity, byName []airport
	for _, ap := range a.all {
		if cc != "" && ap.countryU != cc {
			continue
		}
		switch {
		case ap.cityKey == key:
			byCity = append(byCity, ap)
		case wordPrefix(ap.nameKey, key) && containsWord(ap.nameKey, key):
			byName = append(byName, ap)
		}
	}
	if len(byCity) > 0 {
		return byCity
	}
	return byName
}

// containsWord reports whether key appears in s as whole words.
func containsWord(s, key string) bool {
	for _, sep := range []string{" ", "-", "/", ","} {
		s = strings.ReplaceAll(s, sep, " ")
	}
	return strings.Contains(" "+s+" ", " "+key+" ")
}

func pickAirport(cands []airport, lat, lon *float64) airport {
	best := cands[0]
	bestDist := math.Inf(1)
	for _, c := range cands {
		d := math.Inf(1)
		if lat != nil && lon != nil {
			d = geo.HaversineKm(*lat, *lon, c.Lat, c.Lon)
		}
		if c.size > best.size || (c.size == best.size && d < bestDist) {
			best, bestDist = c, d
		}
	}
	return best
}
