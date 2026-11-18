package transit

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/placematch"
	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
)

const (
	// Source labels this provider in the external-request metrics.
	Source = "overpass"

	railRadiusM    = 800
	busRadiusM     = 400
	amenityRadiusM = 500
	// lodgingRadiusM is how far from the city centre a named hotel is looked
	// for: far enough for a big city's outer districts.
	lodgingRadiusM = 12000

	queryTimeoutS = 12

	// Stops and shops move slowly; what is around a point is reused for a month.
	ttlSurroundings = 30 * 24 * time.Hour
	ttlPlaceHit     = 30 * 24 * time.Hour
	ttlPlaceMiss    = 24 * time.Hour

	// Coordinates in cache keys are rounded to ~11 m, like pkg/routing.
	coordPrecision = 1e4

	maxStopsPerMode = 4
	maxStops        = 12
	maxLines        = 12

	// Two map entries this close are the same building mapped twice (a node
	// and its outline), not two hotels.
	sameBuildingM = 60
)

// OverpassConfig configures the client.
type OverpassConfig struct {
	// BaseURL is the interpreter endpoint, e.g.
	// https://overpass.example.org/api/interpreter. Empty disables the client:
	// every call answers ErrUnavailable.
	BaseURL string
	// FallbackURL is tried once when BaseURL fails. Optional.
	FallbackURL string
}

// Overpass reads stops, everyday places and named lodgings from OpenStreetMap
// through a server that speaks the Overpass query language.
type Overpass struct {
	cfg    OverpassConfig
	client *httpx.Client
	cache  cachestore.Store
	budget *routing.DailyBudget
	now    func() time.Time
}

// NewOverpass builds the client. A nil cache disables caching; a nil budget
// means no daily cap.
func NewOverpass(cfg OverpassConfig, client *httpx.Client, cache cachestore.Store, budget *routing.DailyBudget) *Overpass {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.FallbackURL = strings.TrimSpace(cfg.FallbackURL)
	return &Overpass{cfg: cfg, client: client, cache: cache, budget: budget, now: time.Now}
}

// Enabled reports whether a server is configured.
func (o *Overpass) Enabled() bool { return o != nil && o.cfg.BaseURL != "" }

type element struct {
	Type   string                      `json:"type"`
	ID     int64                       `json:"id"`
	Lat    float64                     `json:"lat"`
	Lon    float64                     `json:"lon"`
	Center *struct{ Lat, Lon float64 } `json:"center"`
	Tags   map[string]string           `json:"tags"`
}

func (e element) point() (LatLon, bool) {
	switch {
	case e.Center != nil:
		return LatLon{Lat: e.Center.Lat, Lon: e.Center.Lon}, true
	case e.Lat != 0 || e.Lon != 0:
		return LatLon{Lat: e.Lat, Lon: e.Lon}, true
	}
	return LatLon{}, false
}

type response struct {
	Elements []element `json:"elements"`
	// Remark is set when the server gave up part-way (timeout, memory): the
	// elements are then incomplete and must not be cached as the answer.
	Remark string `json:"remark"`
}

func (o *Overpass) run(ctx context.Context, query string) ([]element, error) {
	if !o.Enabled() {
		return nil, fmt.Errorf("%w: no map server configured", ErrUnavailable)
	}
	if !o.budget.Take() {
		return nil, fmt.Errorf("%w: daily budget spent", ErrUnavailable)
	}
	body := []byte(url.Values{"data": {query}}.Encode())
	var lastErr error
	for _, base := range []string{o.cfg.BaseURL, o.cfg.FallbackURL} {
		if base == "" {
			continue
		}
		raw, err := o.client.Post(ctx, Source, base, "application/x-www-form-urlencoded", body, nil)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		var resp response
		if err := json.Unmarshal(raw, &resp); err != nil {
			lastErr = fmt.Errorf("decode: %w", err)
			continue
		}
		if strings.Contains(resp.Remark, "runtime error") {
			lastErr = fmt.Errorf("server gave up: %s", clip(resp.Remark, 120))
			continue
		}
		return resp.Elements, nil
	}
	return nil, fmt.Errorf("%w: %w", ErrUnavailable, lastErr)
}

// surroundingsQuery asks for the everyday places first, then a delimiter,
// then each stop followed by the route relations that call at it (directly,
// or through the stop area it belongs to, which is how metro stations are
// usually mapped). The output order is what ties a line to its stop.
func surroundingsQuery(p LatLon) string {
	at := fmt.Sprintf("%.5f,%.5f", p.Lat, p.Lon)
	return fmt.Sprintf(`[out:json][timeout:%d];
(
  nwr(around:%d,%s)[amenity~"^(restaurant|cafe|bar|pub|pharmacy)$"];
  nwr(around:%d,%s)[shop~"^(supermarket|convenience)$"];
);
out tags center 400;
make delim;
out;
(
  node(around:%d,%s)[railway~"^(station|halt|tram_stop)$"];
  node(around:%d,%s)[station~"^(subway|light_rail)$"];
  node(around:%d,%s)[highway=bus_stop];
  node(around:%d,%s)[amenity=ferry_terminal];
)->.stops;
foreach.stops->.s(
  .s out body;
  rel(bn.s)[public_transport=stop_area]->.sa;
  node(r.sa)->.sp;
  (rel(bn.s)[type=route]; rel(bn.sp)[type=route];);
  out tags;
);`, queryTimeoutS,
		amenityRadiusM, at, amenityRadiusM, at,
		railRadiusM, at, railRadiusM, at, busRadiusM, at, railRadiusM, at)
}

// Around returns the stops and everyday places near a point.
func (o *Overpass) Around(ctx context.Context, p LatLon) (Surroundings, error) {
	key := fmt.Sprintf("transit:overpass:v1:around:%.4f,%.4f", round(p.Lat), round(p.Lon))
	var cached Surroundings
	if o.cacheGet(key, &cached) {
		return cached, nil
	}
	els, err := o.run(ctx, surroundingsQuery(p))
	if err != nil {
		return Surroundings{}, err
	}
	out := parseSurroundings(els, p)
	out.FetchedAt = o.now().UTC()
	o.cacheSet(key, out, ttlSurroundings)
	return out, nil
}

func parseSurroundings(els []element, origin LatLon) Surroundings {
	split := len(els)
	for i, e := range els {
		if e.Type == "delim" {
			split = i
			break
		}
	}
	out := Surroundings{Highlights: highlights(els[:split], origin)}
	if split < len(els) {
		out.Stops = stops(els[split+1:], origin)
	}
	return out
}

var highlightOrder = []string{"eat", "bars", "groceries", "pharmacies"}

func highlightKind(tags map[string]string) string {
	switch tags["amenity"] {
	case "restaurant", "cafe":
		return "eat"
	case "bar", "pub":
		return "bars"
	case "pharmacy":
		return "pharmacies"
	}
	switch tags["shop"] {
	case "supermarket", "convenience":
		return "groceries"
	}
	return ""
}

func highlights(els []element, origin LatLon) []Highlight {
	by := map[string]*Highlight{}
	for _, e := range els {
		kind := highlightKind(e.Tags)
		pt, ok := e.point()
		if kind == "" || !ok {
			continue
		}
		d := DistanceM(origin, pt)
		h := by[kind]
		if h == nil {
			h = &Highlight{Kind: kind, NearestM: math.MaxInt}
			by[kind] = h
		}
		h.Count++
		if d < h.NearestM {
			h.NearestM, h.NearestName = d, clip(strings.TrimSpace(e.Tags["name"]), 80)
		}
	}
	out := make([]Highlight, 0, len(by))
	for _, kind := range highlightOrder {
		if h := by[kind]; h != nil {
			out = append(out, *h)
		}
	}
	return out
}

func stopMode(tags map[string]string) Mode {
	switch {
	case tags["station"] == "subway", tags["subway"] == "yes":
		return ModeMetro
	case tags["railway"] == "tram_stop", tags["station"] == "light_rail", tags["tram"] == "yes" && tags["railway"] != "station":
		return ModeTram
	case tags["railway"] == "station", tags["railway"] == "halt":
		return ModeTrain
	case tags["highway"] == "bus_stop":
		return ModeBus
	case tags["amenity"] == "ferry_terminal":
		return ModeFerry
	}
	return ModeUnspecified
}

// routeServes reports whether a route relation of this kind is one a stop of
// this mode would list. A metro station's stop area often holds the bus
// platforms outside it too; their lines are not the station's.
func routeServes(route string, m Mode) bool {
	switch m {
	case ModeMetro:
		return route == "subway" || route == "light_rail"
	case ModeTrain:
		return route == "train" || route == "light_rail" || route == "subway"
	case ModeTram:
		return route == "tram" || route == "light_rail"
	case ModeBus:
		return route == "bus" || route == "trolleybus"
	case ModeFerry:
		return route == "ferry"
	}
	return false
}

var lineSplit = regexp.MustCompile(`[;,\s]+`)

func stops(els []element, origin LatLon) []Stop {
	type key struct {
		name string
		mode Mode
	}
	type acc struct {
		stop  Stop
		lines map[string]bool
	}
	by := map[key]*acc{}
	var order []key
	var cur *acc

	for _, e := range els {
		if e.Type == "node" {
			cur = nil
			mode := stopMode(e.Tags)
			name := strings.TrimSpace(e.Tags["name"])
			pt, ok := e.point()
			if mode == ModeUnspecified || name == "" || !ok {
				continue
			}
			k := key{placematch.Fold(name), mode}
			a := by[k]
			d := DistanceM(origin, pt)
			if a == nil {
				a = &acc{stop: Stop{Name: clip(name, 120), Mode: mode, Lat: pt.Lat, Lon: pt.Lon, DistanceM: d}, lines: map[string]bool{}}
				by[k] = a
				order = append(order, k)
			} else if d < a.stop.DistanceM {
				// The same stop mapped once per direction: keep the nearer side.
				a.stop.Lat, a.stop.Lon, a.stop.DistanceM = pt.Lat, pt.Lon, d
			}
			// Bus stops often carry their lines themselves.
			for _, tag := range []string{"route_ref", "lines"} {
				for _, l := range lineSplit.Split(e.Tags[tag], -1) {
					if l = strings.TrimSpace(l); l != "" && len(l) <= 12 {
						a.lines[l] = true
					}
				}
			}
			cur = a
			continue
		}
		if e.Type != "relation" || cur == nil || e.Tags["type"] != "route" || !routeServes(e.Tags["route"], cur.stop.Mode) {
			continue
		}
		if ref := strings.TrimSpace(e.Tags["ref"]); ref != "" && len(ref) <= 12 {
			cur.lines[ref] = true
		}
	}

	perMode := map[Mode]int{}
	all := make([]Stop, 0, len(order))
	for _, k := range order {
		a := by[k]
		for l := range a.lines {
			a.stop.Lines = append(a.stop.Lines, l)
		}
		sort.Slice(a.stop.Lines, func(i, j int) bool { return lineLess(a.stop.Lines[i], a.stop.Lines[j]) })
		if len(a.stop.Lines) > maxLines {
			a.stop.Lines = a.stop.Lines[:maxLines]
		}
		all = append(all, a.stop)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].DistanceM < all[j].DistanceM })
	out := make([]Stop, 0, maxStops)
	for _, s := range all {
		if perMode[s.Mode] >= maxStopsPerMode || len(out) >= maxStops {
			continue
		}
		perMode[s.Mode]++
		out = append(out, s)
	}
	return out
}

// lineLess sorts "L3" before "L11" and "22" before "V17": by leading letters,
// then by number.
func lineLess(a, b string) bool {
	pa, na := splitLine(a)
	pb, nb := splitLine(b)
	if pa != pb {
		return pa < pb
	}
	if na != nb {
		return na < nb
	}
	return a < b
}

func splitLine(s string) (prefix string, n int) {
	i := 0
	for i < len(s) && (s[i] < '0' || s[i] > '9') {
		i++
	}
	prefix = strings.ToUpper(s[:i])
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
	}
	return prefix, n
}

// lodgingWords say what kind of place a name is, not which one.
var lodgingWords = map[string]bool{
	"hotel": true, "hotels": true, "hostel": true, "hostal": true, "boutique": true, "suites": true, "suite": true,
	"apartments": true, "apartment": true, "apartamentos": true, "apartaments": true, "aparthotel": true,
	"resort": true, "spa": true, "inn": true, "by": true, "and": true, "the": true, "guesthouse": true, "house": true, "casa": true,
}

// FindLodging looks for a hotel, hostel, guest house or holiday apartment by
// name near a point (the city centre). It answers false rather than guess:
// the name must match closely.
func (o *Overpass) FindLodging(ctx context.Context, name string, near LatLon) (Place, bool, error) {
	word := distinctiveWord(name)
	if word == "" {
		return Place{}, false, nil
	}
	key := fmt.Sprintf("transit:overpass:v1:lodging:%.2f,%.2f:%s", near.Lat, near.Lon, placematch.Fold(name))
	var cached cachedPlace
	if o.cacheGet(key, &cached) {
		return cached.Place, cached.Found, nil
	}
	q := fmt.Sprintf(`[out:json][timeout:%d];
nwr(around:%d,%.5f,%.5f)[tourism~"^(hotel|guest_house|hostel|apartment|motel)$"][name~"%s",i];
out tags center 40;`, queryTimeoutS, lodgingRadiusM, near.Lat, near.Lon, looseRegex(word))
	els, err := o.run(ctx, q)
	if err != nil {
		return Place{}, false, err
	}
	var best Place
	found, bestExtra, tied := false, 0, false
	for _, e := range els {
		pt, ok := e.point()
		cand := strings.TrimSpace(e.Tags["name"])
		if !ok || cand == "" {
			continue
		}
		named, extra := placematch.NamedBy(name, cand, lodgingWords)
		if !named {
			continue
		}
		p := Place{Name: clip(cand, 200), Lat: pt.Lat, Lon: pt.Lon, Address: address(e.Tags), Stars: clip(strings.TrimSpace(e.Tags["stars"]), 10)}
		switch {
		case !found || extra < bestExtra:
			best, found, bestExtra, tied = p, true, extra, false
		case extra == bestExtra && DistanceM(LatLon{best.Lat, best.Lon}, pt) > sameBuildingM:
			// Two places the name fits equally, in different buildings.
			tied = true
		}
	}
	if tied {
		best, found = Place{}, false
	}
	ttl := ttlPlaceMiss
	if found {
		ttl = ttlPlaceHit
	}
	o.cacheSet(key, cachedPlace{Place: best, Found: found}, ttl)
	return best, found, nil
}

// FindAddress looks for a building by street name and house number near a
// point. A holiday flat is often listed under its address ("Verdi 254") and
// is on no map as a hotel; this still says where it is.
func (o *Overpass) FindAddress(ctx context.Context, street, number string, near LatLon) (Place, bool, error) {
	word := distinctiveWord(street)
	number = strings.TrimSpace(number)
	if word == "" || !houseNumber.MatchString(number) {
		return Place{}, false, nil
	}
	key := fmt.Sprintf("transit:overpass:v1:address:%.2f,%.2f:%s:%s", near.Lat, near.Lon, placematch.Fold(street), strings.ToLower(number))
	var cached cachedPlace
	if o.cacheGet(key, &cached) {
		return cached.Place, cached.Found, nil
	}
	q := fmt.Sprintf(`[out:json][timeout:%d];
nwr(around:%d,%.5f,%.5f)["addr:housenumber"~"^%s$",i]["addr:street"~"%s",i];
out tags center 20;`, queryTimeoutS, lodgingRadiusM, near.Lat, near.Lon, number, looseRegex(word))
	els, err := o.run(ctx, q)
	if err != nil {
		return Place{}, false, err
	}
	var best Place
	found, bestDist := false, 0
	streets := map[string]bool{}
	for _, e := range els {
		pt, ok := e.point()
		if !ok {
			continue
		}
		streets[placematch.Fold(e.Tags["addr:street"])] = true
		if d := DistanceM(near, pt); !found || d < bestDist {
			best = Place{Lat: pt.Lat, Lon: pt.Lon, Address: address(e.Tags)}
			found, bestDist = true, d
		}
	}
	// Two different streets with that word and that number: no telling which.
	if len(streets) > 1 {
		best, found = Place{}, false
	}
	ttl := ttlPlaceMiss
	if found {
		ttl = ttlPlaceHit
	}
	o.cacheSet(key, cachedPlace{Place: best, Found: found}, ttl)
	return best, found, nil
}

var houseNumber = regexp.MustCompile(`^[0-9]{1,5}[A-Za-z]?$`)

type cachedPlace struct {
	Place Place `json:"place"`
	Found bool  `json:"found"`
}

func address(tags map[string]string) string {
	street := strings.TrimSpace(tags["addr:street"])
	if street == "" {
		return ""
	}
	parts := []string{strings.TrimSpace(street + " " + tags["addr:housenumber"])}
	if rest := strings.TrimSpace(tags["addr:postcode"] + " " + tags["addr:city"]); rest != "" {
		parts = append(parts, rest)
	}
	return clip(strings.Join(parts, ", "), 300)
}

// distinctiveWord is the longest word of a name that says which place it is.
func distinctiveWord(name string) string {
	best := ""
	for _, w := range strings.FieldsFunc(placematch.Fold(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if lodgingWords[w] || len(w) < 3 {
			continue
		}
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}

var accentClass = map[rune]string{
	'a': "[aàáâãäå]", 'e': "[eèéêë]", 'i': "[iìíîï]", 'o': "[oòóôõö]", 'u': "[uùúûü]",
	'c': "[cç]", 'n': "[nñ]",
}

// looseRegex matches a folded word whatever accents the map spells it with.
// The word holds only letters and digits (distinctiveWord), so nothing in it
// can end the quoted regex it is put into.
func looseRegex(word string) string {
	var b strings.Builder
	for _, r := range word {
		switch {
		case accentClass[r] != "":
			b.WriteString(accentClass[r])
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func round(v float64) float64 { return math.Round(v*coordPrecision) / coordPrecision }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// cacheGet and cacheSet store JSON strings: TieredStore mirrors only string
// values to Redis.
func (o *Overpass) cacheGet(key string, into any) bool {
	if o.cache == nil {
		return false
	}
	v, ok := o.cache.Get(key)
	if !ok {
		return false
	}
	s, ok := v.(string)
	if !ok {
		return false
	}
	return json.Unmarshal([]byte(s), into) == nil
}

func (o *Overpass) cacheSet(key string, v any, ttl time.Duration) {
	if o.cache == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	o.cache.Set(key, string(b), ttl)
}
