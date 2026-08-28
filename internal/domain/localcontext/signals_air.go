package localcontext

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const openMeteoAirQualityBaseURL = "https://air-quality-api.open-meteo.com"

// European AQI band boundaries.
//
// The scale is 0-20 good, 20-40 fair, 40-60 moderate, 60-80 poor, 80-100 very
// poor, above 100 extremely poor. Verified against live readings while this was
// written: Lisbon sat at 27-37, Jakarta at 86-95, Delhi at 112-228.
const (
	aqiPoor          = 60
	aqiVeryPoor      = 80
	aqiExtremelyPoor = 100

	// defaultAirQualityThreshold is where we start saying something.
	//
	// Deliberately at "poor" rather than lower. Air quality differs from the
	// other sources in that it always has a value — every destination on earth
	// has an AQI every day — so a low threshold would attach an alert to every
	// trip forever and the alert list would stop meaning "something is up".
	defaultAirQualityThreshold = aqiPoor
)

// AirQualitySource warns when the air at a destination is genuinely bad.
//
// Two shape decisions worth knowing about:
//
// It emits at most ONE alert per trip, naming the worst day, rather than one per
// day. The scorer charges per alert, so a five-day trip through smoke would
// otherwise be penalised five times for what is one fact about one place.
//
// It leaves the alert unlocated. Air quality describes the destination itself
// rather than an event somewhere near it, so a map pin would land on top of the
// city marker and say nothing the destination does not already say. Hazards are
// located because they are elsewhere; this is not.
type AirQualitySource struct {
	baseURL   string
	client    *httpx.Client
	threshold float64

	cache *signalCache
}

// airDay is the daily fold of the hourly series.
// Exported JSON tags because these are marshalled into the shared cache;
// unexported fields would round-trip as zero values.
type airDay struct {
	Date    time.Time `json:"date"`
	MaxAQI  float64   `json:"max_aqi"`
	MaxPM25 float64   `json:"max_pm25"`
}

// NewAirQualitySource builds the source. An empty baseURL uses the public
// endpoint; a threshold of zero or less uses the default.
func NewAirQualitySource(
	baseURL string,
	client *httpx.Client,
	threshold float64,
	cache *signalCache,
) *AirQualitySource {
	if baseURL == "" {
		baseURL = openMeteoAirQualityBaseURL
	}
	if threshold <= 0 {
		threshold = defaultAirQualityThreshold
	}
	return &AirQualitySource{baseURL: baseURL, client: client, threshold: threshold, cache: cache}
}

func (s *AirQualitySource) Name() string { return SourceAirQuality }

// openMeteoAirResponse is the hourly block.
//
// Open-Meteo exposes no daily aggregate for AQI — `daily=european_aqi_max` is
// rejected outright — so the hourly series is folded here. Values are nullable
// pointers because the series carries gaps.
type openMeteoAirResponse struct {
	Hourly struct {
		Time        []string   `json:"time"`
		EuropeanAQI []*float64 `json:"european_aqi"`
		PM25        []*float64 `json:"pm2_5"`
	} `json:"hourly"`
}

func (s *AirQualitySource) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	days, err := s.forecast(ctx, req.Lat, req.Lon)
	if err != nil {
		return nil, err
	}

	// The worst day inside the trip window is what a traveller needs to know.
	var worst airDay
	for _, d := range days {
		if !withinWindow(d.Date, req.Start, req.End) {
			continue
		}
		if d.MaxAQI > worst.MaxAQI {
			worst = d
		}
	}

	if worst.MaxAQI < s.threshold {
		// Good air is not news. Saying so on every clean trip would train users
		// to ignore the alert list.
		return nil, nil
	}

	d := worst.Date
	detail := fmt.Sprintf("European AQI %.0f (%s) on %s",
		worst.MaxAQI, aqiBandLabel(worst.MaxAQI), d.Format("Mon 2 Jan"))
	if worst.MaxPM25 > 0 {
		detail += fmt.Sprintf(", PM2.5 %.0f µg/m³", worst.MaxPM25)
	}

	return []Alert{{
		Kind:     AlertAirQuality,
		Title:    fmt.Sprintf("%s air quality expected", aqiBandLabel(worst.MaxAQI)),
		Detail:   detail,
		Date:     &d,
		Severity: aqiSeverity(worst.MaxAQI),
		Source:   SourceAirQuality,
	}}, nil
}

func (s *AirQualitySource) forecast(ctx context.Context, lat, lon float64) ([]airDay, error) {
	// Rounded to ~1km, the same key the weather adapters use, so a trip view
	// asking repeatedly about one city costs one call.
	key := fmt.Sprintf("%.2f,%.2f", lat, lon)

	if cached, ok := cacheGet[[]airDay](s.cache, SourceAirQuality, key); ok {
		return cached, nil
	}

	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	q.Set("hourly", "european_aqi,pm2_5")
	q.Set("timezone", "UTC")
	q.Set("forecast_days", "5")

	endpoint := s.baseURL + "/v1/air-quality?" + q.Encode()
	body, err := httpx.GetJSON[openMeteoAirResponse](ctx, s.client, SourceAirQuality, endpoint)
	if err != nil {
		return nil, err
	}

	days, err := body.toDailyMax()
	if err != nil {
		return nil, err
	}

	cacheSet(s.cache, SourceAirQuality, key, days, ttlAirQuality)
	return days, nil
}

// toDailyMax folds the hourly series into a per-day maximum.
//
// Maximum rather than mean on purpose: a day that is clean all morning and
// hazardous all afternoon is a day you would want warning about, and averaging
// hides exactly that.
func (r openMeteoAirResponse) toDailyMax() ([]airDay, error) {
	h := r.Hourly
	if len(h.Time) == 0 {
		return nil, fmt.Errorf("open-meteo air quality: no hourly series in response")
	}

	byDay := make(map[string]*airDay)
	var order []string

	for i, ts := range h.Time {
		if len(ts) < 10 {
			continue
		}
		key := ts[:10]

		d := byDay[key]
		if d == nil {
			parsed, err := time.Parse("2006-01-02", key)
			if err != nil {
				continue
			}
			d = &airDay{Date: parsed}
			byDay[key] = d
			order = append(order, key)
		}

		if i < len(h.EuropeanAQI) {
			if v := h.EuropeanAQI[i]; v != nil && *v > d.MaxAQI {
				d.MaxAQI = *v
			}
		}
		if i < len(h.PM25) {
			if v := h.PM25[i]; v != nil && *v > d.MaxPM25 {
				d.MaxPM25 = *v
			}
		}
	}

	if len(order) == 0 {
		return nil, fmt.Errorf("open-meteo air quality: no usable hours in response")
	}

	// order is already ascending because the API returns time ascending.
	out := make([]airDay, 0, len(order))
	for _, key := range order {
		out = append(out, *byDay[key])
	}
	return out, nil
}

// aqiSeverity grades an AQI reading.
//
// Anything below the alerting threshold never reaches this, so the lowest band
// here is already "poor".
func aqiSeverity(aqi float64) Severity {
	switch {
	case aqi >= aqiExtremelyPoor:
		return SeverityMajor
	case aqi >= aqiVeryPoor:
		return SeverityModerate
	default:
		return SeverityMinor
	}
}

// aqiBandLabel names the band in the European AQI's own vocabulary, so the text
// a user reads matches what they will find on any official air-quality site.
func aqiBandLabel(aqi float64) string {
	switch {
	case aqi >= aqiExtremelyPoor:
		return "Extremely poor"
	case aqi >= aqiVeryPoor:
		return "Very poor"
	case aqi >= aqiPoor:
		return "Poor"
	case aqi >= 40:
		return "Moderate"
	case aqi >= 20:
		return "Fair"
	default:
		return "Good"
	}
}
