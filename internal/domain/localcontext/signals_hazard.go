package localcontext

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const (
	gdacsBaseURL = "https://www.gdacs.org"
	usgsBaseURL  = "https://earthquake.usgs.gov"

	// defaultHazardRadiusKm bounds "near the destination". Generous, because a
	// wildfire or a cyclone affects travel — smoke, closed roads, cancelled
	// flights — well beyond where it is burning. The distance is always stated
	// in the alert so a traveller can judge for themselves.
	defaultHazardRadiusKm = 500

	// usgsMinMagnitude and usgsLookback filter earthquakes down to ones that
	// actually bear on a trip.
	//
	// An earthquake is a past event, not a forecast, so unlike weather it is
	// only relevant when it was recent AND significant: aftershocks, damaged
	// infrastructure, closed sites. Without these thresholds the source would
	// report a M2.6 tremor 400km offshore three months ago and penalise a trip
	// for it.
	usgsMinMagnitude = 4.5
	usgsLookback     = 7 * 24 * time.Hour
)

// --- GDACS -----------------------------------------------------------------

// GDACSSource surfaces current global disasters — wildfires, tropical cyclones,
// floods, earthquakes, volcanoes, droughts — from GDACS.
//
// The single most useful signal here for a traveller, and the one that most
// justifies this whole feature: a wildfire or a cyclone near a destination
// changes whether you should go, and nothing else in the app knew about it.
//
// GDACS publishes one global list rather than a location query, so this fetches
// everything once, caches it, and filters by distance locally. That is cheaper
// than it sounds — the list is ~100 events — and means five cities on a compare
// page cost one upstream call, not five.
type GDACSSource struct {
	baseURL  string
	client   *httpx.Client
	radiusKm float64

	mu    sync.Mutex
	cache []gdacsFeature
	at    time.Time
	ttl   time.Duration
	now   func() time.Time
}

func NewGDACSSource(baseURL string, client *httpx.Client, radiusKm float64) *GDACSSource {
	if baseURL == "" {
		baseURL = gdacsBaseURL
	}
	if radiusKm <= 0 {
		radiusKm = defaultHazardRadiusKm
	}
	return &GDACSSource{
		baseURL:  baseURL,
		client:   client,
		radiusKm: radiusKm,
		ttl:      30 * time.Minute,
		now:      time.Now,
	}
}

func (s *GDACSSource) Name() string { return SourceGDACS }

type gdacsResponse struct {
	Features []gdacsFeature `json:"features"`
}

// gdacsFeature is deliberately a minimal subset.
//
// GDACS types its JSON loosely — `iscurrent`, for instance, is the *string*
// "true" rather than a boolean, which decodes into a bool field as an error and
// takes the whole response with it. So: decode only the fields actually used,
// and prefer string with local parsing over trusting a type. Anything added
// here should be checked against a live payload first, not assumed.
type gdacsFeature struct {
	Geometry struct {
		// GeoJSON order is [longitude, latitude]. Reading these the wrong way
		// round puts every European hazard in the Indian Ocean, which the
		// distance filter would then silently drop.
		Coordinates []float64 `json:"coordinates"`
	} `json:"geometry"`
	Properties struct {
		EventType    string `json:"eventtype"`
		AlertLevel   string `json:"alertlevel"`
		Name         string `json:"name"`
		Country      string `json:"country"`
		FromDate     string `json:"fromdate"`
		ToDate       string `json:"todate"`
		SeverityData struct {
			SeverityText string `json:"severitytext"`
		} `json:"severitydata"`
	} `json:"properties"`
}

func (s *GDACSSource) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	features, err := s.events(ctx)
	if err != nil {
		return nil, err
	}

	var out []Alert
	for _, f := range features {
		if len(f.Geometry.Coordinates) < 2 {
			continue
		}
		lon, lat := f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]

		distKm := geo.HaversineKm(req.Lat, req.Lon, lat, lon)
		if distKm > s.radiusKm {
			continue
		}

		from := parseGDACSTime(f.Properties.FromDate)
		to := parseGDACSTime(f.Properties.ToDate)
		// A hazard has a duration, so the test is whether it overlaps the trip
		// rather than whether a single instant falls inside it.
		if !windowsOverlap(from, to, req.Start, req.End) {
			continue
		}

		out = append(out, f.toAlert(lat, lon, distKm))
	}
	return out, nil
}

func (s *GDACSSource) events(ctx context.Context) ([]gdacsFeature, error) {
	s.mu.Lock()
	if s.cache != nil && s.now().Sub(s.at) < s.ttl {
		cached := s.cache
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()

	endpoint := s.baseURL + "/gdacsapi/api/events/geteventlist/EVENTS4APP"
	body, err := httpx.GetJSON[gdacsResponse](ctx, s.client, SourceGDACS, endpoint)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache = body.Features
	s.at = s.now()
	s.mu.Unlock()

	return body.Features, nil
}

func (f gdacsFeature) toAlert(lat, lon, distKm float64) Alert {
	p := f.Properties

	title := p.Name
	if title == "" {
		title = gdacsEventLabel(p.EventType)
		if p.Country != "" {
			title += " in " + p.Country
		}
	}

	parts := []string{fmt.Sprintf("about %.0f km away", distKm)}
	if p.SeverityData.SeverityText != "" {
		parts = append(parts, p.SeverityData.SeverityText)
	}
	if lvl := strings.ToLower(p.AlertLevel); lvl != "" {
		parts = append(parts, lvl+" alert level")
	}

	from := parseGDACSTime(p.FromDate)
	var date *time.Time
	if !from.IsZero() {
		d := from
		date = &d
	}

	return Alert{
		Kind:     AlertHazard,
		Title:    title,
		Detail:   strings.Join(parts, " · "),
		Date:     date,
		Severity: gdacsSeverity(p.AlertLevel),
		Source:   SourceGDACS,
		Lat:      &lat,
		Lon:      &lon,
	}
}

// gdacsSeverity maps GDACS's own alert level onto ours.
//
// Leaning on their triage rather than inventing one is the point: GDACS already
// weighs magnitude against population exposure, which is the judgement that
// decides whether an event actually disrupts anything.
func gdacsSeverity(level string) Severity {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "red":
		return SeverityMajor
	case "orange":
		return SeverityModerate
	default:
		// Green, and anything unrecognised. Most of the global list is green at
		// any moment; treating those as major would put every destination on
		// earth permanently under a warning.
		return SeverityMinor
	}
}

func gdacsEventLabel(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "EQ":
		return "Earthquake"
	case "TC":
		return "Tropical cyclone"
	case "FL":
		return "Flood"
	case "WF":
		return "Wildfire"
	case "VO":
		return "Volcanic activity"
	case "DR":
		return "Drought"
	default:
		return "Hazard"
	}
}

// parseGDACSTime reads GDACS's naive local timestamps. A malformed or absent
// date yields the zero time, which the overlap check treats as "unbounded"
// rather than excluding the event — a hazard with no date is still a hazard.
func parseGDACSTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05",
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// --- USGS ------------------------------------------------------------------

// USGSSource surfaces recent significant earthquakes near a destination.
//
// Narrower than GDACS on purpose. GDACS lists globally notable events; this
// catches a locally significant quake during or just before a trip that the
// global list would not rank. The Gatherer dedupes where the two overlap.
type USGSSource struct {
	baseURL  string
	client   *httpx.Client
	radiusKm float64
	now      func() time.Time
}

func NewUSGSSource(baseURL string, client *httpx.Client, radiusKm float64) *USGSSource {
	if baseURL == "" {
		baseURL = usgsBaseURL
	}
	if radiusKm <= 0 {
		radiusKm = defaultHazardRadiusKm
	}
	return &USGSSource{baseURL: baseURL, client: client, radiusKm: radiusKm, now: time.Now}
}

func (s *USGSSource) Name() string { return SourceUSGS }

type usgsResponse struct {
	Features []usgsFeature `json:"features"`
}

type usgsFeature struct {
	Geometry struct {
		Coordinates []float64 `json:"coordinates"` // [lon, lat, depth]
	} `json:"geometry"`
	Properties struct {
		Mag   float64 `json:"mag"`
		Place string  `json:"place"`
		Time  int64   `json:"time"` // epoch milliseconds
		Alert string  `json:"alert"`
		Title string  `json:"title"`
	} `json:"properties"`
}

func (s *USGSSource) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	// Unlike GDACS this API takes a location query, so the filtering happens
	// upstream and there is nothing to cache globally.
	now := s.now().UTC()
	q := url.Values{}
	q.Set("format", "geojson")
	q.Set("latitude", fmt.Sprintf("%f", req.Lat))
	q.Set("longitude", fmt.Sprintf("%f", req.Lon))
	q.Set("maxradiuskm", fmt.Sprintf("%.0f", s.radiusKm))
	q.Set("starttime", now.Add(-usgsLookback).Format("2006-01-02"))
	q.Set("minmagnitude", fmt.Sprintf("%.1f", usgsMinMagnitude))
	q.Set("limit", "20")
	q.Set("orderby", "magnitude")

	endpoint := s.baseURL + "/fdsnws/event/1/query?" + q.Encode()
	body, err := httpx.GetJSON[usgsResponse](ctx, s.client, SourceUSGS, endpoint)
	if err != nil {
		return nil, err
	}

	var out []Alert
	for _, f := range body.Features {
		if len(f.Geometry.Coordinates) < 2 {
			continue
		}
		lon, lat := f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]
		when := time.UnixMilli(f.Properties.Time).UTC()
		distKm := geo.HaversineKm(req.Lat, req.Lon, lat, lon)

		place := f.Properties.Place
		if place == "" {
			place = f.Properties.Title
		}

		d := when
		out = append(out, Alert{
			Kind:  AlertHazard,
			Title: fmt.Sprintf("Magnitude %.1f earthquake — %s", f.Properties.Mag, place),
			Detail: fmt.Sprintf("about %.0f km away, %s",
				distKm, humanAgo(now.Sub(when))),
			Date:     &d,
			Severity: quakeSeverity(f.Properties.Mag, f.Properties.Alert),
			Source:   SourceUSGS,
			Lat:      &lat,
			Lon:      &lon,
		})
	}
	return out, nil
}

// quakeSeverity grades a quake by magnitude, letting USGS's own PAGER alert
// override when they have issued one.
//
// PAGER already accounts for depth, population and building stock, which
// magnitude alone does not: a M6 far offshore disrupts nothing, a M5 under a
// city disrupts a great deal.
func quakeSeverity(mag float64, pagerAlert string) Severity {
	switch strings.ToLower(strings.TrimSpace(pagerAlert)) {
	case "red", "orange":
		return SeverityMajor
	case "yellow":
		return SeverityModerate
	}
	switch {
	case mag >= 6.0:
		return SeverityMajor
	case mag >= 5.0:
		return SeverityModerate
	default:
		return SeverityMinor
	}
}

func humanAgo(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "within the last hour"
	case d < 24*time.Hour:
		return fmt.Sprintf("%.0f hours ago", d.Hours())
	default:
		days := int(math.Round(d.Hours() / 24))
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// windowsOverlap reports whether two date ranges intersect. A zero bound means
// unbounded on that side, so an event with no dates matches any trip and a trip
// with no dates matches any event.
func windowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	if !aEnd.IsZero() && !bStart.IsZero() && aEnd.Before(bStart) {
		return false
	}
	if !bEnd.IsZero() && !aStart.IsZero() && bEnd.Before(aStart) {
		return false
	}
	return true
}
