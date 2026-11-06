package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const (
	// Source labels this provider in the external-request metrics.
	Source = "openrouteservice"

	orsBaseURL = "https://api.openrouteservice.org"

	// Streets change slowly; a leg is reused for a month. Most pairs inside a
	// city repeat across travellers, which is what makes a 2,000-a-day free
	// quota go a long way.
	ttlLeg = 30 * 24 * time.Hour
	// "No accessible route" is cached too, so a traveller re-asking does not
	// spend budget on the same refusal; short, because the map gets fixed.
	ttlNoRoute = 24 * time.Hour

	// coordPrecision rounds cache-key coordinates to ~11 m: the same café
	// geocoded twice must hit the same entry.
	coordPrecision = 1e4
)

// OpenRouteService is a PedestrianRouter backed by the hosted
// OpenRouteService directions API (HeiGIT).
type OpenRouteService struct {
	baseURL string
	apiKey  string
	client  *httpx.Client
	cache   cachestore.Store
	budget  *DailyBudget
}

// NewOpenRouteService builds a router. An empty baseURL uses the hosted API; a
// nil cache disables caching; a nil budget means no daily cap.
func NewOpenRouteService(baseURL, apiKey string, client *httpx.Client, cache cachestore.Store, budget *DailyBudget) *OpenRouteService {
	if baseURL == "" {
		baseURL = orsBaseURL
	}
	return &OpenRouteService{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(apiKey),
		client:  client,
		cache:   cache,
		budget:  budget,
	}
}

type orsRequest struct {
	Coordinates  [][2]float64 `json:"coordinates"`
	Elevation    bool         `json:"elevation"`
	Instructions bool         `json:"instructions"`
	Options      *orsOptions  `json:"options,omitempty"`
}

type orsOptions struct {
	AvoidFeatures []string          `json:"avoid_features,omitempty"`
	ProfileParams *orsProfileParams `json:"profile_params,omitempty"`
}

type orsProfileParams struct {
	Restrictions map[string]any `json:"restrictions,omitempty"`
}

type orsResponse struct {
	Routes []struct {
		Summary struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
			Ascent   float64 `json:"ascent"`
		} `json:"summary"`
		Warnings []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"warnings"`
	} `json:"routes"`
}

type orsError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ORS error codes that mean "there is no such route", not "we failed".
// 2009: route could not be found; 2010: point not found (no routable way near
// a coordinate — for a wheelchair, often a place reachable only by steps).
const (
	orsCodeRouteNotFound = 2009
	orsCodePointNotFound = 2010
)

// Route implements PedestrianRouter.
func (o *OpenRouteService) Route(ctx context.Context, from, to LatLon, opts Options) (Leg, error) {
	if opts.Profile == "" {
		opts.Profile = ProfileWalking
	}
	key := legCacheKey(from, to, opts)
	if leg, noRoute, ok := o.cacheGet(key); ok {
		if noRoute {
			return Leg{}, ErrNoRoute
		}
		return leg, nil
	}
	if !o.budget.Take() {
		return Leg{}, fmt.Errorf("%w: daily budget spent", ErrUnavailable)
	}

	url := fmt.Sprintf("%s/v2/directions/%s/json", o.baseURL, opts.Profile)
	resp, err := httpx.PostJSON[orsResponse](ctx, o.client, Source, url, buildORSRequest(from, to, opts),
		http.Header{"Authorization": {o.apiKey}})
	if err != nil {
		if isNoRoute(err) {
			o.cacheSet(key, cachedLeg{NoRoute: true}, ttlNoRoute)
			return Leg{}, ErrNoRoute
		}
		return Leg{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if len(resp.Routes) == 0 {
		o.cacheSet(key, cachedLeg{NoRoute: true}, ttlNoRoute)
		return Leg{}, ErrNoRoute
	}

	r := resp.Routes[0]
	leg := Leg{DistanceM: r.Summary.Distance, DurationS: r.Summary.Duration, AscentM: r.Summary.Ascent}
	for _, w := range r.Warnings {
		if msg := strings.TrimSpace(w.Message); msg != "" {
			leg.Warnings = append(leg.Warnings, msg)
		}
	}
	o.cacheSet(key, cachedLeg{Leg: leg}, ttlLeg)
	return leg, nil
}

func buildORSRequest(from, to LatLon, opts Options) orsRequest {
	req := orsRequest{
		// ORS takes [lon, lat], the GeoJSON order, not [lat, lon].
		Coordinates:  [][2]float64{{from.Lon, from.Lat}, {to.Lon, to.Lat}},
		Elevation:    true,
		Instructions: false,
	}
	o := &orsOptions{}
	if opts.AvoidSteps {
		o.AvoidFeatures = []string{"steps"}
	}
	if opts.Profile == ProfileWheelchair {
		r := map[string]any{}
		if opts.MaxInclinePct > 0 {
			r["maximum_incline"] = opts.MaxInclinePct
		}
		if opts.MaxSlopedKerbM > 0 {
			r["maximum_sloped_kerb"] = opts.MaxSlopedKerbM
		}
		if opts.PavedOnly {
			r["surface_type"] = "paved"
			r["smoothness_type"] = "good"
		}
		if len(r) > 0 {
			o.ProfileParams = &orsProfileParams{Restrictions: r}
		}
	}
	if o.AvoidFeatures != nil || o.ProfileParams != nil {
		req.Options = o
	}
	return req
}

// isNoRoute reports whether ORS answered "no such route" rather than failing.
func isNoRoute(err error) bool {
	var se *httpx.StatusError
	if !errors.As(err, &se) || (se.Status != http.StatusNotFound && se.Status != http.StatusBadRequest) {
		return false
	}
	var body orsError
	if json.Unmarshal([]byte(se.Body), &body) != nil {
		return false
	}
	return body.Error.Code == orsCodeRouteNotFound || body.Error.Code == orsCodePointNotFound
}

type cachedLeg struct {
	Leg     Leg  `json:"leg"`
	NoRoute bool `json:"no_route,omitempty"`
}

func legCacheKey(from, to LatLon, opts Options) string {
	r := func(v float64) float64 { return math.Round(v*coordPrecision) / coordPrecision }
	return fmt.Sprintf("routing:ors:v1:%s:%.4f,%.4f:%.4f,%.4f:i%d:k%.2f:s%t:p%t",
		opts.Profile, r(from.Lat), r(from.Lon), r(to.Lat), r(to.Lon),
		opts.MaxInclinePct, opts.MaxSlopedKerbM, opts.AvoidSteps, opts.PavedOnly)
}

// cacheGet and cacheSet store JSON strings: TieredStore mirrors only string
// values to Redis, so a struct would silently stop being shared.
func (o *OpenRouteService) cacheGet(key string) (Leg, bool, bool) {
	if o.cache == nil {
		return Leg{}, false, false
	}
	v, ok := o.cache.Get(key)
	if !ok {
		return Leg{}, false, false
	}
	s, ok := v.(string)
	if !ok {
		return Leg{}, false, false
	}
	var c cachedLeg
	if json.Unmarshal([]byte(s), &c) != nil {
		return Leg{}, false, false
	}
	return c.Leg, c.NoRoute, true
}

func (o *OpenRouteService) cacheSet(key string, c cachedLeg, ttl time.Duration) {
	if o.cache == nil {
		return
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	o.cache.Set(key, string(b), ttl)
}
