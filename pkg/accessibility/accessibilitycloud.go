package accessibility

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/placematch"
)

const (
	// Source labels this provider in the external-request metrics.
	Source = "accessibility-cloud"

	// The cached host the provider asks clients to use; the uncached one is
	// only for reading back a fresh edit.
	accessibilityCloudBaseURL = "https://accessibility-cloud-v2.freetls.fastly.net"

	// searchRadiusM is how far from the place's coordinate to look. A POI's
	// coordinate (LLM-written, then canonicalised) and the dataset's can sit
	// on different sides of a building.
	searchRadiusM = 60
	// matchRadiusM is the furthest a record may be and still count as the
	// same place when its name also matches.
	matchRadiusM = 40.0
	// sameSpotRadiusM is close enough to accept without a name match: two
	// different venues rarely share a 10 m spot.
	sameSpotRadiusM = 10.0
	// minNameSimilarity is the token-overlap score two names need to match.
	minNameSimilarity = 0.6

	ttlHit  = 7 * 24 * time.Hour
	ttlMiss = 24 * time.Hour
)

// AccessibilityCloud is a PlaceAccessibilitySource backed by the
// accessibility.cloud place-infos API.
type AccessibilityCloud struct {
	baseURL  string
	appToken string
	client   *httpx.Client
	cache    cachestore.Store
}

// NewAccessibilityCloud builds a source. An empty baseURL uses the provider's
// cached host; a nil cache disables caching.
func NewAccessibilityCloud(baseURL, appToken string, client *httpx.Client, cache cachestore.Store) *AccessibilityCloud {
	if baseURL == "" {
		baseURL = accessibilityCloudBaseURL
	}
	return &AccessibilityCloud{
		baseURL:  strings.TrimRight(baseURL, "/"),
		appToken: strings.TrimSpace(appToken),
		client:   client,
		cache:    cache,
	}
}

type placeInfosResponse struct {
	Features []struct {
		Properties struct {
			Name          json.RawMessage `json:"name"`
			Distance      float64         `json:"distance"`
			SourceID      string          `json:"sourceId"`
			UpdatedAt     string          `json:"updatedAt"`
			Accessibility *struct {
				AccessibleWith *struct {
					Wheelchair *bool `json:"wheelchair"`
				} `json:"accessibleWith"`
				PartiallyAccessibleWith *struct {
					Wheelchair *bool `json:"wheelchair"`
				} `json:"partiallyAccessibleWith"`
				Entrances []struct {
					Properties *entranceProps `json:"properties"`
					// Some datasets flatten the entrance properties.
					entranceProps
				} `json:"entrances"`
				Restrooms []struct {
					IsAccessibleWithWheelchair *bool `json:"isAccessibleWithWheelchair"`
				} `json:"restrooms"`
			} `json:"accessibility"`
		} `json:"properties"`
	} `json:"features"`
	Related struct {
		Sources map[string]struct {
			Name      string `json:"name"`
			LicenseID string `json:"licenseId"`
		} `json:"sources"`
		Licenses map[string]struct {
			Name      string `json:"name"`
			ShortName string `json:"shortName"`
		} `json:"licenses"`
	} `json:"related"`
}

type entranceProps struct {
	IsLevel      *bool `json:"isLevel"`
	HasFixedRamp *bool `json:"hasFixedRamp"`
}

// Lookup implements PlaceAccessibilitySource.
func (a *AccessibilityCloud) Lookup(ctx context.Context, lat, lon float64, name string) (*PlaceFacts, error) {
	if lat == 0 && lon == 0 {
		return nil, nil
	}
	key := cacheKey(lat, lon, name)
	if facts, ok := a.cacheGet(key); ok {
		return facts, nil
	}

	q := url.Values{}
	q.Set("appToken", a.appToken)
	q.Set("latitude", fmt.Sprintf("%.6f", lat))
	q.Set("longitude", fmt.Sprintf("%.6f", lon))
	q.Set("accuracy", fmt.Sprintf("%d", searchRadiusM))
	q.Set("limit", "20")
	q.Set("includeRelated", "source,source.license")
	resp, err := httpx.GetJSON[placeInfosResponse](ctx, a.client, Source, a.baseURL+"/place-infos.json?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	facts := pickMatch(resp, name)
	ttl := ttlHit
	if facts == nil {
		ttl = ttlMiss
	}
	a.cacheSet(key, facts, ttl)
	return facts, nil
}

// pickMatch returns the facts of the nearest record that is plausibly the
// same place, or nil. A wrong match is worse than none — it would tell a
// wheelchair user the café next door's entrance is level — so the rules err
// towards nil.
func pickMatch(resp placeInfosResponse, name string) *PlaceFacts {
	var best *PlaceFacts
	bestDist := math.Inf(1)
	for _, f := range resp.Features {
		p := f.Properties
		if p.Accessibility == nil || p.Distance > matchRadiusM || p.Distance >= bestDist {
			continue
		}
		if p.Distance > sameSpotRadiusM && nameSimilarity(name, featureName(p.Name)) < minNameSimilarity {
			continue
		}

		facts := &PlaceFacts{}
		acc := p.Accessibility
		switch {
		case acc.AccessibleWith != nil && acc.AccessibleWith.Wheelchair != nil && *acc.AccessibleWith.Wheelchair:
			facts.Wheelchair = WheelchairYes
		case acc.PartiallyAccessibleWith != nil && acc.PartiallyAccessibleWith.Wheelchair != nil && *acc.PartiallyAccessibleWith.Wheelchair:
			facts.Wheelchair = WheelchairLimited
		case acc.AccessibleWith != nil && acc.AccessibleWith.Wheelchair != nil:
			facts.Wheelchair = WheelchairNo
		}
		for _, e := range acc.Entrances {
			props := e.entranceProps
			if e.Properties != nil {
				props = *e.Properties
			}
			if props.IsLevel != nil && facts.EntranceLevel == nil {
				facts.EntranceLevel = props.IsLevel
			}
			if props.HasFixedRamp != nil && facts.EntranceRamp == nil {
				facts.EntranceRamp = props.HasFixedRamp
			}
		}
		for _, r := range acc.Restrooms {
			if r.IsAccessibleWithWheelchair != nil {
				facts.AccessibleToilet = r.IsAccessibleWithWheelchair
				break
			}
		}
		if facts.Empty() {
			continue
		}
		if src, ok := resp.Related.Sources[p.SourceID]; ok {
			facts.SourceName = src.Name
			if lic, ok := resp.Related.Licenses[src.LicenseID]; ok {
				facts.Licence = lic.ShortName
				if facts.Licence == "" {
					facts.Licence = lic.Name
				}
			}
		}
		if t, err := time.Parse(time.RFC3339, p.UpdatedAt); err == nil {
			facts.ObservedAt = &t
		}
		best, bestDist = facts, p.Distance
	}
	return best
}

// featureName reads a record's name, which datasets give either as a string
// or as a {lang: name} map.
func featureName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) == nil {
		for _, k := range []string{"en", "und", ""} {
			if v := m[k]; v != "" {
				return v
			}
		}
		for _, v := range m {
			return v
		}
	}
	return ""
}

// nameSimilarity is placematch.NameSimilarity; the indirection keeps this
// file's call sites and tests unchanged.
func nameSimilarity(a, b string) float64 { return placematch.NameSimilarity(a, b) }

func tokens(s string) map[string]bool { return placematch.Tokens(s) }

func cacheKey(lat, lon float64, name string) string {
	return fmt.Sprintf("a11y:ac:v1:%.4f,%.4f:%s", lat, lon, strings.Join(sortedTokens(name), "-"))
}

func sortedTokens(name string) []string {
	t := tokens(name)
	out := make([]string, 0, len(t))
	for w := range t {
		out = append(out, w)
	}
	// Insertion sort: names have a handful of words.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

type cachedFacts struct {
	Facts *PlaceFacts `json:"facts"`
}

// cacheGet and cacheSet store JSON strings so Redis mirrors them.
func (a *AccessibilityCloud) cacheGet(key string) (*PlaceFacts, bool) {
	if a.cache == nil {
		return nil, false
	}
	v, ok := a.cache.Get(key)
	if !ok {
		return nil, false
	}
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	var c cachedFacts
	if json.Unmarshal([]byte(s), &c) != nil {
		return nil, false
	}
	return c.Facts, true
}

func (a *AccessibilityCloud) cacheSet(key string, facts *PlaceFacts, ttl time.Duration) {
	if a.cache == nil {
		return
	}
	b, err := json.Marshal(cachedFacts{Facts: facts})
	if err != nil {
		return
	}
	a.cache.Set(key, string(b), ttl)
}
