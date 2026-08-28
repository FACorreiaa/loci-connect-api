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
	CountryCode string `json:"countryCode"`
	CountryName string `json:"countryName"`
}

func (g *BigDataCloudGeocoder) CountryCode(ctx context.Context, lat, lon float64) (string, error) {
	key := fmt.Sprintf("%.1f,%.1f", lat, lon)

	if code, ok := cacheGet[string](g.cache, SourceGeocode, key); ok {
		return code, nil
	}

	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	q.Set("localityLanguage", "en")

	endpoint := g.baseURL + "/data/reverse-geocode-client?" + q.Encode()
	body, err := httpx.GetJSON[bigDataCloudResponse](ctx, g.client, SourceGeocode, endpoint)
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
