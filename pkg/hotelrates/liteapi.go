package hotelrates

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/placematch"
)

const (
	// Source labels LiteAPI in the external-request metrics.
	Source = "liteapi"
	// ProviderName is what a Rate reports as its provider.
	ProviderName = "liteapi"

	liteAPIBaseURL = "https://api.liteapi.travel/v3.0"

	// searchRadiusM is how far around the LLM's coordinate to look; LiteAPI's
	// minimum. LLM-written coordinates can be a few blocks out.
	searchRadiusM = 1000
	// matchRadiusM is the furthest a candidate may be and still count as the
	// same hotel when its name matches.
	matchRadiusM = 400.0
	// minNameSimilarity is the token-overlap score two names need to match
	// with coordinates; without them the bar is higher.
	minNameSimilarity       = 0.6
	minNameSimilarityNoGeo  = 0.8
	matchConcurrency        = 4
	providerTimeoutSeconds  = 8
	defaultGuestNationality = "US"

	ttlMatchHit  = 7 * 24 * time.Hour
	ttlMatchMiss = 24 * time.Hour
	// ttlRate keeps a shown price from drifting far from the bookable one.
	ttlRate = 15 * time.Minute
)

// genericWords are dropped before comparing hotel names: "Hotel Arts" and
// "Arts Barcelona" are the same place, and "Hotel" says nothing.
var genericWords = map[string]bool{
	"hotel": true, "hotels": true, "hostel": true, "boutique": true,
	"suites": true, "suite": true, "apartments": true, "apartamentos": true,
	"resort": true, "spa": true, "inn": true, "by": true, "and": true, "y": true, "e": true,
}

// LiteAPIConfig configures the LiteAPI provider.
type LiteAPIConfig struct {
	// BaseURL overrides the API host (tests); empty uses production.
	BaseURL string
	APIKey  string
	// WhitelabelURL is the booking site deep links point at, e.g.
	// https://book.lociai.fyi. Empty leaves BookingURL blank: prices still
	// show, there is just nowhere to send the click.
	WhitelabelURL string
	// Margin overrides the account's markup, in percent. Nil keeps the
	// account default.
	Margin *float64
	// GuestNationality is the ISO-2 code rates are quoted for; LiteAPI
	// requires one. Empty uses US.
	GuestNationality string
}

// LiteAPI is a Provider backed by LiteAPI (Nuitée).
type LiteAPI struct {
	cfg    LiteAPIConfig
	client *httpx.Client
	cache  cachestore.Store
	now    func() time.Time
}

// NewLiteAPI builds the provider; a nil cache disables caching.
func NewLiteAPI(cfg LiteAPIConfig, client *httpx.Client, cache cachestore.Store) *LiteAPI {
	if cfg.BaseURL == "" {
		cfg.BaseURL = liteAPIBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	cfg.WhitelabelURL = strings.TrimRight(strings.TrimSpace(cfg.WhitelabelURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.GuestNationality = strings.ToUpper(strings.TrimSpace(cfg.GuestNationality))
	if cfg.GuestNationality == "" {
		cfg.GuestNationality = defaultGuestNationality
	}
	return &LiteAPI{cfg: cfg, client: client, cache: cache, now: time.Now}
}

func (l *LiteAPI) header() http.Header {
	h := http.Header{}
	h.Set("X-API-Key", l.cfg.APIKey)
	return h
}

// Rates implements Provider.
func (l *LiteAPI) Rates(ctx context.Context, hotels []Query, stay Stay) ([]Rate, error) {
	out := Unmatched(hotels)
	if len(hotels) == 0 {
		return out, nil
	}
	currency := strings.ToUpper(stay.Currency)

	// 1. Match every hotel to a LiteAPI id.
	ids := make([]string, len(hotels))
	errs := make([]error, len(hotels))
	var wg sync.WaitGroup
	sem := make(chan struct{}, matchConcurrency)
	for i, h := range hotels {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ids[i], errs[i] = l.match(ctx, h)
		}()
	}
	wg.Wait()
	if allFailed(errs) {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(errs...))
	}

	// 2. Price the matched ids, from cache where fresh.
	priced := map[string]cachedRate{}
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if r, ok := l.cachedRate(rateKey(id, stay, currency)); ok {
			priced[id] = r
		} else {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fresh, err := l.fetchRates(ctx, missing, stay, currency)
		if err != nil {
			return nil, err
		}
		for _, id := range missing {
			r, ok := fresh[id]
			if !ok {
				r = cachedRate{Status: StatusSoldOut, FetchedAt: l.now()}
			}
			priced[id] = r
			l.cacheSet(rateKey(id, stay, currency), r, ttlRate)
		}
	}

	// 3. Assemble, in query order.
	nights := max(1, stay.Nights())
	for i, id := range ids {
		if id == "" {
			continue
		}
		r := priced[id]
		rate := Rate{Key: hotels[i].Key, Status: r.Status, Provider: ProviderName, FetchedAt: r.FetchedAt}
		if r.Status == StatusPriced {
			rate.Currency = r.Currency
			rate.TotalMinor = r.TotalMinor
			rate.NightlyMinor = (r.TotalMinor + int64(nights)/2) / int64(nights)
			rate.BookingURL = l.bookingURL(id, stay, r.Currency)
		}
		out[i] = rate
	}
	return out, nil
}

func allFailed(errs []error) bool {
	for _, err := range errs {
		if err == nil {
			return false
		}
	}
	return len(errs) > 0
}

// ---- matching ----

type liteHotel struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	City      string  `json:"city"`
	Country   string  `json:"country"`
}

type hotelsResponse struct {
	Data []liteHotel `json:"data"`
}

// match returns the LiteAPI id for a hotel, or "" when no candidate is
// plausibly the same place. A provider error is returned (and not cached) so
// a blip does not hide a hotel's price for a day.
func (l *LiteAPI) match(ctx context.Context, q Query) (string, error) {
	hasGeo := q.Lat != 0 || q.Lon != 0
	ignore := ignoreFor(q.City)
	if len(placematch.Tokens(q.Name, ignore)) == 0 {
		return "", nil
	}
	if !hasGeo && (q.City == "" || q.CountryCode == "") {
		return "", nil
	}

	key := matchKey(q, ignore)
	if id, ok := l.cachedMatch(key); ok {
		return id, nil
	}

	// A narrow search by the name's most distinctive word first; then, with
	// coordinates, everything nearby, for names the LLM spelled differently.
	attempts := []url.Values{l.hotelsQuery(q, hasGeo, distinctiveWord(q.Name, ignore))}
	if hasGeo {
		attempts = append(attempts, l.hotelsQuery(q, hasGeo, ""))
	}
	for _, params := range attempts {
		resp, err := httpx.GetJSONWithHeader[hotelsResponse](ctx, l.client, Source, l.cfg.BaseURL+"/data/hotels?"+params.Encode(), l.header())
		if err != nil {
			var se *httpx.StatusError
			if errors.As(err, &se) && se.Status >= 400 && se.Status < 500 && se.Status != http.StatusTooManyRequests {
				// The query itself was refused (e.g. a country LiteAPI does not
				// know): no match, and asking again will not change that.
				break
			}
			return "", err
		}
		if id := pickHotel(q, resp.Data, hasGeo, ignore); id != "" {
			l.cacheSet(key, cachedMatch{ID: id}, ttlMatchHit)
			return id, nil
		}
	}
	l.cacheSet(key, cachedMatch{}, ttlMatchMiss)
	return "", nil
}

func (l *LiteAPI) hotelsQuery(q Query, hasGeo bool, name string) url.Values {
	v := url.Values{}
	if q.CountryCode != "" {
		v.Set("countryCode", strings.ToUpper(q.CountryCode))
	}
	if hasGeo {
		v.Set("latitude", strconv.FormatFloat(q.Lat, 'f', 6, 64))
		v.Set("longitude", strconv.FormatFloat(q.Lon, 'f', 6, 64))
		v.Set("radius", strconv.Itoa(searchRadiusM))
	} else {
		v.Set("cityName", q.City)
	}
	if name != "" {
		v.Set("hotelName", name)
	}
	v.Set("limit", "200")
	return v
}

// pickHotel returns the best candidate's id, or "". Highest name similarity
// wins, nearest breaks ties; anything below the bar loses outright.
func pickHotel(q Query, candidates []liteHotel, hasGeo bool, ignore map[string]bool) string {
	best, bestSim, bestDist := "", 0.0, 0.0
	for _, c := range candidates {
		if c.ID == "" {
			continue
		}
		sim := placematch.NameSimilarity(q.Name, c.Name, ignore)
		dist := 0.0
		if hasGeo {
			dist = geo.HaversineKm(q.Lat, q.Lon, c.Latitude, c.Longitude) * 1000
			if sim < minNameSimilarity || dist > matchRadiusM {
				continue
			}
		} else if sim < minNameSimilarityNoGeo || placematch.Fold(c.City) != placematch.Fold(q.City) {
			continue
		}
		if best == "" || sim > bestSim || (sim == bestSim && dist < bestDist) {
			best, bestSim, bestDist = c.ID, sim, dist
		}
	}
	return best
}

// ignoreFor is genericWords plus the city's own words: "Barcelona" in
// "W Barcelona" does not tell two Barcelona hotels apart.
func ignoreFor(city string) map[string]bool {
	ig := make(map[string]bool, len(genericWords)+2)
	for w := range genericWords {
		ig[w] = true
	}
	for w := range placematch.Tokens(city) {
		ig[w] = true
	}
	return ig
}

// distinctiveWord is the name's longest meaningful word, used to narrow the
// provider's search. Accents are kept off: the provider's loose match is
// accent-sensitive, and a folded word still matches an unaccented name.
func distinctiveWord(name string, ignore map[string]bool) string {
	best := ""
	for w := range placematch.Tokens(name, ignore) {
		if len(w) > len(best) || (len(w) == len(best) && w < best) {
			best = w
		}
	}
	if len(best) < 3 {
		return ""
	}
	return best
}

func matchKey(q Query, ignore map[string]bool) string {
	words := make([]string, 0)
	for w := range placematch.Tokens(q.Name, ignore) {
		words = append(words, w)
	}
	sort.Strings(words)
	return fmt.Sprintf("hotelrates:lite:v1:match:%.4f,%.4f:%s:%s:%s",
		q.Lat, q.Lon, strings.ToUpper(q.CountryCode), placematch.Fold(q.City), strings.Join(words, "-"))
}

// ---- pricing ----

type occupancy struct {
	Adults int `json:"adults"`
}

type ratesRequest struct {
	HotelIDs         []string    `json:"hotelIds"`
	Checkin          string      `json:"checkin"`
	Checkout         string      `json:"checkout"`
	Occupancies      []occupancy `json:"occupancies"`
	Currency         string      `json:"currency"`
	GuestNationality string      `json:"guestNationality"`
	Timeout          int         `json:"timeout"`
	MaxRatesPerHotel int         `json:"maxRatesPerHotel"`
	Margin           *float64    `json:"margin,omitempty"`
}

// amount is a LiteAPI price. The schema types offer prices as arrays while
// responses send single objects, so both are accepted; an array is summed.
type amount struct {
	Amount   float64
	Currency string
	Valid    bool
}

type rawAmount struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

func (a *amount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '[' {
		var list []rawAmount
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		for _, r := range list {
			if a.Currency != "" && r.Currency != a.Currency {
				return fmt.Errorf("mixed currencies %q and %q", a.Currency, r.Currency)
			}
			a.Amount += r.Amount
			a.Currency = r.Currency
			a.Valid = true
		}
		return nil
	}
	var r rawAmount
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*a = amount{Amount: r.Amount, Currency: r.Currency, Valid: true}
	return nil
}

type ratesResponse struct {
	Data []struct {
		HotelID   string `json:"hotelId"`
		RoomTypes []struct {
			OfferRetailRate amount `json:"offerRetailRate"`
			Rates           []struct {
				RetailRate struct {
					Total amount `json:"total"`
				} `json:"retailRate"`
			} `json:"rates"`
		} `json:"roomTypes"`
	} `json:"data"`
}

// fetchRates prices ids in one call. A hotel missing from the answer has no
// availability for the stay.
func (l *LiteAPI) fetchRates(ctx context.Context, ids []string, stay Stay, currency string) (map[string]cachedRate, error) {
	req := ratesRequest{
		HotelIDs:         ids,
		Checkin:          stay.Checkin.Format(time.DateOnly),
		Checkout:         stay.Checkout.Format(time.DateOnly),
		Occupancies:      []occupancy{{Adults: stay.Adults}},
		Currency:         currency,
		GuestNationality: l.cfg.GuestNationality,
		Timeout:          providerTimeoutSeconds,
		MaxRatesPerHotel: 1,
		Margin:           l.cfg.Margin,
	}
	resp, err := httpx.PostJSON[ratesResponse](ctx, l.client, Source, l.cfg.BaseURL+"/hotels/rates", req, l.header())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	now := l.now()
	out := map[string]cachedRate{}
	for _, h := range resp.Data {
		var cheapest *amount
		for _, rt := range h.RoomTypes {
			price := rt.OfferRetailRate
			if !price.Valid {
				// Older responses only carry per-room totals.
				for _, r := range rt.Rates {
					if r.RetailRate.Total.Valid {
						price.Amount += r.RetailRate.Total.Amount
						price.Currency = r.RetailRate.Total.Currency
						price.Valid = true
					}
				}
			}
			if !price.Valid || price.Amount <= 0 {
				continue
			}
			if cheapest == nil || price.Amount < cheapest.Amount {
				p := price
				cheapest = &p
			}
		}
		if cheapest == nil {
			continue
		}
		cur := strings.ToUpper(cheapest.Currency)
		if cur == "" {
			cur = currency
		}
		out[h.HotelID] = cachedRate{
			Status:     StatusPriced,
			TotalMinor: ToMinor(cheapest.Amount, cur),
			Currency:   cur,
			FetchedAt:  now,
		}
	}
	return out, nil
}

// bookingURL deep-links the white-label hotel page for the stay. Occupancy
// is a base64 JSON array, as the white-label expects.
func (l *LiteAPI) bookingURL(id string, stay Stay, currency string) string {
	if l.cfg.WhitelabelURL == "" {
		return ""
	}
	occ, _ := json.Marshal([]occupancy{{Adults: stay.Adults}})
	v := url.Values{}
	v.Set("checkin", stay.Checkin.Format(time.DateOnly))
	v.Set("checkout", stay.Checkout.Format(time.DateOnly))
	v.Set("occupancies", base64.StdEncoding.EncodeToString(occ))
	v.Set("currency", currency)
	v.Set("clientReference", "loci")
	return l.cfg.WhitelabelURL + "/hotels/" + url.PathEscape(id) + "?" + v.Encode()
}

func rateKey(id string, stay Stay, currency string) string {
	return fmt.Sprintf("hotelrates:lite:v1:rate:%s:%s:%s:%d:%s", id,
		stay.Checkin.Format(time.DateOnly), stay.Checkout.Format(time.DateOnly), stay.Adults, currency)
}

// ---- cache (JSON strings, so Redis mirrors them) ----

type cachedMatch struct {
	ID string `json:"id"`
}

type cachedRate struct {
	Status     Status    `json:"status"`
	TotalMinor int64     `json:"total_minor"`
	Currency   string    `json:"currency"`
	FetchedAt  time.Time `json:"fetched_at"`
}

func (l *LiteAPI) cachedMatch(key string) (string, bool) {
	var m cachedMatch
	if !l.cacheGet(key, &m) {
		return "", false
	}
	return m.ID, true
}

func (l *LiteAPI) cachedRate(key string) (cachedRate, bool) {
	var r cachedRate
	ok := l.cacheGet(key, &r)
	return r, ok
}

func (l *LiteAPI) cacheGet(key string, into any) bool {
	if l.cache == nil {
		return false
	}
	v, ok := l.cache.Get(key)
	if !ok {
		return false
	}
	s, ok := v.(string)
	if !ok {
		return false
	}
	return json.Unmarshal([]byte(s), into) == nil
}

func (l *LiteAPI) cacheSet(key string, v any, ttl time.Duration) {
	if l.cache == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	l.cache.Set(key, string(b), ttl)
}
