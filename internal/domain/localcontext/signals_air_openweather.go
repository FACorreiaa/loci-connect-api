package localcontext

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const openWeatherAirBaseURL = "https://api.openweathermap.org"

// SourceAirQualityOW names the provider in metrics and on the wire.
const SourceAirQualityOW = "openweather-air"

// OpenWeatherAirSource is the air-quality source for deployments running on
// OpenWeather.
//
// It exists for a licensing reason rather than a technical one. Open-Meteo's
// free tier is non-commercial and Loci sells subscriptions, so a production
// deployment either pays Open-Meteo or uses OpenWeather — and switching only
// the *forecast* provider would leave air quality still calling Open-Meteo on
// every trip view. This closes that hole using the key OpenWeather already
// needs.
//
// Same contract as the Open-Meteo source: at most one alert per trip, naming
// the worst day, and unlocated because air quality describes the destination
// itself rather than an event near it.
type OpenWeatherAirSource struct {
	baseURL string
	apiKey  string
	client  *httpx.Client
	minBand AQBand
	cache   *signalCache
}

// NewOpenWeatherAirSource builds the source. An empty baseURL uses the public
// endpoint. An empty apiKey makes Fetch a no-op rather than an error: the
// source is only wired when a key exists, and failing loudly for a missing
// optional provider would bench it for nothing.
func NewOpenWeatherAirSource(
	baseURL, apiKey string,
	client *httpx.Client,
	minBand AQBand,
	cache *signalCache,
) *OpenWeatherAirSource {
	if baseURL == "" {
		baseURL = openWeatherAirBaseURL
	}
	return &OpenWeatherAirSource{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  client,
		minBand: minBand,
		cache:   cache,
	}
}

func (s *OpenWeatherAirSource) Name() string { return SourceAirQualityOW }

// openWeatherAirResponse is the forecast payload.
//
// Both the current and forecast endpoints return the same shape; the forecast
// one simply has many entries in `list`. Every numeric field is a pointer so a
// missing one reads as absent rather than as zero — the mistake that would turn
// "no data" into "perfect air".
type openWeatherAirResponse struct {
	List []struct {
		Dt   int64 `json:"dt"`
		Main struct {
			// OpenWeather's own index, 1-5. NOT the European AQI: a 4 here is
			// "poor", where a 4 on Open-Meteo's scale is pristine.
			AQI *int `json:"aqi"`
		} `json:"main"`
		Components struct {
			PM25 *float64 `json:"pm2_5"`
		} `json:"components"`
	} `json:"list"`
}

func (s *OpenWeatherAirSource) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	if s.apiKey == "" {
		return nil, nil
	}

	days, err := s.forecast(ctx, req.Lat, req.Lon)
	if err != nil {
		return nil, err
	}

	var worst airDay
	var worstBand AQBand
	found := false
	for _, d := range days {
		if !withinWindow(d.Date, req.Start, req.End) {
			continue
		}
		b := bandForOpenWeatherAQI(int(d.MaxAQI))
		if !found || b > worstBand {
			worst, worstBand, found = d, b, true
		}
	}

	if !found || worstBand < s.minBand {
		return nil, nil
	}

	d := worst.Date
	detail := fmt.Sprintf("OpenWeather air quality index %.0f of 5 (%s) on %s",
		worst.MaxAQI, worstBand.Label(), d.Format("Mon 2 Jan"))
	if worst.MaxPM25 > 0 {
		detail += fmt.Sprintf(", PM2.5 %.0f µg/m³", worst.MaxPM25)
	}

	return []Alert{{
		Kind:     AlertAirQuality,
		Title:    fmt.Sprintf("%s air quality expected", worstBand.Label()),
		Detail:   detail,
		Date:     &d,
		Severity: worstBand.Severity(),
		Source:   SourceAirQualityOW,
	}}, nil
}

func (s *OpenWeatherAirSource) forecast(ctx context.Context, lat, lon float64) ([]airDay, error) {
	key := fmt.Sprintf("%.2f,%.2f", lat, lon)
	if cached, ok := cacheGet[[]airDay](s.cache, SourceAirQualityOW, key); ok {
		return cached, nil
	}

	q := url.Values{}
	q.Set("lat", fmt.Sprintf("%f", lat))
	q.Set("lon", fmt.Sprintf("%f", lon))
	q.Set("appid", s.apiKey)

	endpoint := s.baseURL + "/data/2.5/air_pollution/forecast?" + q.Encode()
	body, err := httpx.GetJSON[openWeatherAirResponse](ctx, s.client, SourceAirQualityOW, endpoint)
	if err != nil {
		return nil, err
	}

	days, err := body.toDailyMax()
	if err != nil {
		return nil, err
	}

	cacheSet(s.cache, SourceAirQualityOW, key, days, ttlAirQuality)
	return days, nil
}

// toDailyMax folds the hourly list into a per-day worst reading.
//
// Maximum rather than mean, for the same reason as the Open-Meteo source: a day
// that is clean all morning and hazardous all afternoon is the day worth
// warning about, and averaging hides exactly that.
func (r openWeatherAirResponse) toDailyMax() ([]airDay, error) {
	if len(r.List) == 0 {
		return nil, fmt.Errorf("openweather air: no entries in response")
	}

	byDay := make(map[string]*airDay)
	var order []string

	for _, e := range r.List {
		if e.Dt <= 0 || e.Main.AQI == nil {
			continue
		}
		ts := time.Unix(e.Dt, 0).UTC()
		key := ts.Format("2006-01-02")

		d := byDay[key]
		if d == nil {
			d = &airDay{Date: ts.Truncate(24 * time.Hour)}
			byDay[key] = d
			order = append(order, key)
		}
		if v := float64(*e.Main.AQI); v > d.MaxAQI {
			d.MaxAQI = v
		}
		if e.Components.PM25 != nil && *e.Components.PM25 > d.MaxPM25 {
			d.MaxPM25 = *e.Components.PM25
		}
	}

	if len(order) == 0 {
		return nil, fmt.Errorf("openweather air: no usable entries in response")
	}

	out := make([]airDay, 0, len(order))
	for _, key := range order {
		out = append(out, *byDay[key])
	}
	return out, nil
}

// bandForOpenWeatherAQI maps OpenWeather's 1-5 index onto the shared band.
//
// Their scale tops out at 5, so "extremely poor" is unreachable here — a Delhi
// reading that Open-Meteo puts at 228 (extremely poor) arrives as a 5. The
// band, and therefore the go-score penalty, is capped at very poor rather than
// pretending to a precision the provider does not offer.
func bandForOpenWeatherAQI(aqi int) AQBand {
	switch {
	case aqi >= 5:
		return AQVeryPoor
	case aqi == 4:
		return AQPoor
	case aqi == 3:
		return AQModerate
	case aqi == 2:
		return AQFair
	default:
		return AQGood
	}
}
