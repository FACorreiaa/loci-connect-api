package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

const (
	// Source labels this provider in the external-request metrics. It is part
	// of the observable contract — dashboards and alerts match on it.
	Source = "open-meteo-geocoding"

	openMeteoBaseURL = "https://geocoding-api.open-meteo.com"
	// Paid plans are served from a separate host and require &apikey=, exactly
	// as the forecast endpoint does. Open-Meteo's free tier is non-commercial
	// by their terms, so without this a subscription would still need a code
	// change to actually be used.
	openMeteoCustomerBaseURL = "https://customer-geocoding-api.open-meteo.com"

	// A city's coordinates do not move, so a hit is cached for a long time.
	ttlHit = 30 * 24 * time.Hour
	// A miss is cached too, and this is not an optimisation: the autocomplete
	// calls this per keystroke, so without a negative cache "porto" costs five
	// provider requests for "p", "po", "por", "port", "porto" — four of which
	// may legitimately match nothing. Short enough that a genuinely new place
	// appears within the day.
	ttlMiss = 6 * time.Hour

	defaultLimit = 5
	maxLimit     = 100 // the provider's own ceiling for `count`
)

// OpenMeteoGeocoder is a Forward backed by Open-Meteo's geocoding search.
//
// Chosen because it needs no API key and is the same vendor already serving the
// default forecast, so it adds a hostname rather than a dependency or a bill.
type OpenMeteoGeocoder struct {
	baseURL string
	apiKey  string
	client  *httpx.Client
	cache   cachestore.Store
}

// NewOpenMeteo builds a geocoder.
//
// baseURL is a parameter so tests can point at an httptest server and a
// self-hosted instance can be configured by env, the same reason
// NewOpenMeteoAdapter takes one. An empty apiKey uses the free, non-commercial
// host; supplying one switches to the customer host unless baseURL overrides it.
// A nil cache disables caching.
func NewOpenMeteo(baseURL, apiKey string, client *httpx.Client, cache cachestore.Store) *OpenMeteoGeocoder {
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		baseURL = openMeteoBaseURL
		if apiKey != "" {
			baseURL = openMeteoCustomerBaseURL
		}
	}
	return &OpenMeteoGeocoder{baseURL: baseURL, apiKey: apiKey, client: client, cache: cache}
}

// searchResponse is the wire shape. Unlike the forecast endpoint, which returns
// column arrays, this one returns a list of objects.
//
// `results` is absent entirely when nothing matched — see Search.
type searchResponse struct {
	Results []struct {
		Name        string  `json:"name"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		Country     string  `json:"country"`
		CountryCode string  `json:"country_code"`
		Admin1      string  `json:"admin1"`
		Population  int     `json:"population"`
		FeatureCode string  `json:"feature_code"`
	} `json:"results"`
}

func (g *OpenMeteoGeocoder) Search(ctx context.Context, name string, limit int) ([]Place, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	key := cacheKey(name, limit)
	if places, ok := g.cacheGet(key); ok {
		observability.ExternalCacheHitsTotal.WithLabelValues(Source).Inc()
		return places, nil
	}

	q := url.Values{}
	q.Set("name", name)
	q.Set("count", fmt.Sprintf("%d", limit))
	q.Set("language", "en")
	q.Set("format", "json")
	if g.apiKey != "" {
		q.Set("apikey", g.apiKey)
	}

	endpoint := g.baseURL + "/v1/search?" + q.Encode()
	body, err := httpx.GetJSON[searchResponse](ctx, g.client, Source, endpoint)
	if err != nil {
		if isTransient(err) {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return nil, err
	}

	// A miss is `{"generationtime_ms":0.4}` with no `results` key at all, not an
	// empty array and not an error status. Treating that as a failure would turn
	// every typo into a 500, so zero results is a valid, cacheable answer.
	places := make([]Place, 0, len(body.Results))
	for _, r := range body.Results {
		places = append(places, Place{
			Name:        r.Name,
			Country:     r.Country,
			CountryCode: strings.ToUpper(strings.TrimSpace(r.CountryCode)),
			Admin1:      r.Admin1,
			Lat:         r.Latitude,
			Lon:         r.Longitude,
			Population:  r.Population,
			FeatureCode: r.FeatureCode,
		})
	}

	ttl := ttlHit
	if len(places) == 0 {
		ttl = ttlMiss
	}
	g.cacheSet(key, places, ttl)
	return places, nil
}

// isTransient reports whether an error is the provider's fault rather than the
// request's, and so whether retrying later is the right advice.
func isTransient(err error) bool {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		// 429 and 5xx are the provider failing or throttling us. Every other
		// 4xx means we asked wrongly, and retrying will not help.
		return se.Status == http.StatusTooManyRequests || se.Status >= 500
	}
	// Not a status error at all: a timeout, a DNS failure, a refused
	// connection, or a body that would not decode. All transient.
	return true
}

func cacheKey(name string, limit int) string {
	return fmt.Sprintf("geocode:fwd:%d:%s", limit, strings.ToLower(name))
}

// cacheGet and cacheSet store JSON strings rather than []Place.
//
// TieredStore only mirrors *string* values to Redis; a struct is kept in memory
// only and silently stops being shared between replicas. Encoding here keeps
// the cache working the way the rest of the app assumes it does.
func (g *OpenMeteoGeocoder) cacheGet(key string) ([]Place, bool) {
	if g.cache == nil {
		return nil, false
	}
	raw, ok := g.cache.Get(key)
	if !ok {
		return nil, false
	}
	s, ok := raw.(string)
	if !ok {
		return nil, false
	}
	var places []Place
	if err := json.Unmarshal([]byte(s), &places); err != nil {
		return nil, false
	}
	return places, true
}

func (g *OpenMeteoGeocoder) cacheSet(key string, places []Place, ttl time.Duration) {
	if g.cache == nil {
		return
	}
	b, err := json.Marshal(places)
	if err != nil {
		return
	}
	g.cache.Set(key, string(b), ttl)
}
