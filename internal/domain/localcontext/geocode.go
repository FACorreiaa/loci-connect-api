package localcontext

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const bigDataCloudBaseURL = "https://api.bigdatacloud.net"

// CountryResolver turns coordinates into an ISO-3166-1 alpha-2 country code.
//
// It exists for one reason: holiday and advisory data is published per country,
// but GetLocalContext is called with coordinates. Declared as a narrow
// interface so the holiday source depends on "something that knows the
// country", not on a particular geocoder.
type CountryResolver interface {
	CountryCode(ctx context.Context, lat, lon float64) (string, error)
}

// BigDataCloudGeocoder resolves a country from coordinates using
// BigDataCloud's keyless reverse-geocode endpoint.
//
// Cached far more aggressively than any other source here, because the country
// a coordinate sits in does not change. The key is rounded to one decimal place
// (~11 km) rather than the ~1 km used for weather: a country boundary is not a
// weather front, and a coarser key means a city's worth of lookups collapses to
// one call.
type BigDataCloudGeocoder struct {
	baseURL string
	client  *httpx.Client
	cache   *signalCache
}

// NewBigDataCloudGeocoder builds a geocoder. An empty baseURL uses the public
// endpoint; a nil cache disables caching.
func NewBigDataCloudGeocoder(baseURL string, client *httpx.Client, cache *signalCache) *BigDataCloudGeocoder {
	if baseURL == "" {
		baseURL = bigDataCloudBaseURL
	}
	return &BigDataCloudGeocoder{baseURL: baseURL, client: client, cache: cache}
}

type bigDataCloudResponse struct {
	CountryCode          string `json:"countryCode"`
	CountryName          string `json:"countryName"`
	City                 string `json:"city"`
	Locality             string `json:"locality"`
	PrincipalSubdivision string `json:"principalSubdivision"`
}

// sourcePlace namespaces town-level cache entries apart from the country ones:
// same upstream, different key precision.
const sourcePlace = SourceGeocode + "-place"

// Place is a coordinate named at town level. Any field may be empty.
type Place struct {
	Locality    string
	Region      string
	CountryCode string
	CountryName string
}

// PlaceResolver names the town a coordinate sits in.
type PlaceResolver interface {
	Place(ctx context.Context, lat, lon float64) (Place, error)
}

func (g *BigDataCloudGeocoder) lookup(ctx context.Context, lat, lon float64) (bigDataCloudResponse, error) {
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	q.Set("localityLanguage", "en")

	endpoint := g.baseURL + "/data/reverse-geocode-client?" + q.Encode()
	return httpx.GetJSON[bigDataCloudResponse](ctx, g.client, SourceGeocode, endpoint)
}

// Place names the town. Keyed at 0.01° (~1 km), not the 0.1° CountryCode
// uses: at ~11 km the town is often the neighbouring one.
func (g *BigDataCloudGeocoder) Place(ctx context.Context, lat, lon float64) (Place, error) {
	key := fmt.Sprintf("%.2f,%.2f", lat, lon)
	if p, ok := cacheGet[Place](g.cache, sourcePlace, key); ok {
		return p, nil
	}
	body, err := g.lookup(ctx, lat, lon)
	if err != nil {
		return Place{}, err
	}
	locality := strings.TrimSpace(body.City)
	if locality == "" {
		locality = strings.TrimSpace(body.Locality)
	}
	p := Place{
		Locality:    locality,
		Region:      strings.TrimSpace(body.PrincipalSubdivision),
		CountryCode: strings.ToUpper(strings.TrimSpace(body.CountryCode)),
		CountryName: strings.TrimSpace(body.CountryName),
	}
	cacheSet(g.cache, sourcePlace, key, p, ttlGeocode)
	return p, nil
}

func (g *BigDataCloudGeocoder) CountryCode(ctx context.Context, lat, lon float64) (string, error) {
	key := fmt.Sprintf("%.1f,%.1f", lat, lon)

	if code, ok := cacheGet[string](g.cache, SourceGeocode, key); ok {
		return code, nil
	}

	body, err := g.lookup(ctx, lat, lon)
	if err != nil {
		return "", err
	}

	code := strings.ToUpper(strings.TrimSpace(body.CountryCode))
	if code == "" {
		// Open ocean and Antarctica genuinely have no country. That is a valid
		// answer, not an error — the caller simply skips country-scoped sources.
		return "", nil
	}

	cacheSet(g.cache, SourceGeocode, key, code, ttlGeocode)
	return code, nil
}
