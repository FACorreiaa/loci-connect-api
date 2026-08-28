package localcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const openMeteoBaseURL = "https://api.open-meteo.com"

// openMeteoFetchDays is how many days we always ask for, regardless of what the
// caller wants. Asking for a fixed horizon means one cache entry satisfies every
// window length — a 2-day weekend and a 10-day trip to the same city share it —
// and the payload is a handful of float arrays either way. 16 is the provider's
// maximum and matches the proto's `days` ceiling.
const openMeteoFetchDays = 16

// OpenMeteoAdapter is a WeatherAdapter backed by Open-Meteo's daily forecast.
//
// It exists because it needs **no API key**, which makes it the right default:
// before this, a deployment without OPENWEATHER_API_KEY fell back to
// StubWeather, and every forecast in the app — trip views, /compare columns, the
// go-score and packing suggestions — was a labelled placeholder. Same interface
// as OpenWeatherAdapter, same resilience shape (HTTP timeout + short in-memory
// TTL cache), so it drops in wherever that one was used.
type OpenMeteoAdapter struct {
	baseURL string
	http    *http.Client

	cache *signalCache
}

// NewOpenMeteoAdapter builds an adapter with sane resilience defaults. An empty
// baseURL uses the public endpoint; it is a parameter so tests can point at an
// httptest server and so a self-hosted instance can be configured by env.
func NewOpenMeteoAdapter(baseURL string, cache *signalCache) *OpenMeteoAdapter {
	if baseURL == "" {
		baseURL = openMeteoBaseURL
	}
	return &OpenMeteoAdapter{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 8 * time.Second},
		cache:   cache,
	}
}

func (a *OpenMeteoAdapter) cacheKey(lat, lon float64) string {
	// Round to ~1km so nearby requests share a cache entry, same as OpenWeather.
	return fmt.Sprintf("%.2f,%.2f", lat, lon)
}

func (a *OpenMeteoAdapter) Forecast(ctx context.Context, lat, lon float64, days int) ([]WeatherDay, error) {
	if days <= 0 {
		days = 5
	}

	key := a.cacheKey(lat, lon)
	if cached, ok := cacheGet[[]WeatherDay](a.cache, SourceOpenMeteo, key); ok {
		return clampDays(cached, days), nil
	}

	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max")
	// UTC keeps the day buckets aligned with how OpenWeatherAdapter folds its
	// 3-hour entries, so the two providers cannot disagree about which day a
	// forecast belongs to.
	q.Set("timezone", "UTC")
	q.Set("forecast_days", fmt.Sprintf("%d", openMeteoFetchDays))

	endpoint := a.baseURL + "/v1/forecast?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open-meteo request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open-meteo status %d", resp.StatusCode)
	}

	var body openMeteoResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("open-meteo decode: %w", err)
	}

	daily, err := body.toWeatherDays()
	if err != nil {
		return nil, err
	}

	cacheSet(a.cache, SourceOpenMeteo, key, daily, ttlForecast)
	return clampDays(daily, days), nil
}

// --- Open-Meteo wire types ---

// openMeteoResponse is the daily block. Open-Meteo returns column arrays rather
// than a list of objects, so every array must be the same length as `time` — a
// shorter one means a malformed response, not a missing day.
type openMeteoResponse struct {
	Daily struct {
		Time                        []string  `json:"time"`
		WeatherCode                 []int     `json:"weather_code"`
		TemperatureMax              []float64 `json:"temperature_2m_max"`
		TemperatureMin              []float64 `json:"temperature_2m_min"`
		PrecipitationProbabilityMax []float64 `json:"precipitation_probability_max"`
	} `json:"daily"`
}

func (r openMeteoResponse) toWeatherDays() ([]WeatherDay, error) {
	d := r.Daily
	n := len(d.Time)
	if n == 0 {
		return nil, fmt.Errorf("open-meteo: no daily forecast in response")
	}
	if len(d.WeatherCode) < n || len(d.TemperatureMax) < n ||
		len(d.TemperatureMin) < n || len(d.PrecipitationProbabilityMax) < n {
		return nil, fmt.Errorf("open-meteo: ragged daily arrays (time=%d)", n)
	}

	out := make([]WeatherDay, 0, n)
	for i := 0; i < n; i++ {
		date, err := time.Parse("2006-01-02", d.Time[i])
		if err != nil {
			return nil, fmt.Errorf("open-meteo: bad date %q: %w", d.Time[i], err)
		}
		// Open-Meteo reports probability as a percentage; WeatherDay.PrecipProb
		// is 0..1 and scoreWeather multiplies by it directly, so a missed
		// conversion would silently zero out the weather dimension.
		prob := d.PrecipitationProbabilityMax[i] / 100
		if prob < 0 {
			prob = 0
		}
		if prob > 1 {
			prob = 1
		}
		out = append(out, WeatherDay{
			Date:       date,
			HighC:      d.TemperatureMax[i],
			LowC:       d.TemperatureMin[i],
			Condition:  conditionForWMOCode(d.WeatherCode[i]),
			PrecipProb: prob,
		})
	}
	return out, nil
}

// conditionForWMOCode maps a WMO weather code to the same vocabulary
// OpenWeather uses ("Clear", "Clouds", "Rain", "Snow", …).
//
// This mapping is load-bearing beyond display: isWet() decides a day is wet by
// substring-matching "rain", "storm" and "snow", and scoreWeather penalises wet
// days. Returning "Precipitation" or a raw code number here would make every
// rainy day score as dry. Drizzle and fog are deliberately NOT in that
// vocabulary — a drizzly day is not a washout, and such days still count as wet
// when their precipitation probability alone crosses isWet's 0.5 threshold.
func conditionForWMOCode(code int) string {
	switch code {
	case 0, 1:
		return "Clear"
	case 2, 3:
		return "Clouds"
	case 45, 48:
		return "Fog"
	case 51, 53, 55, 56, 57:
		return "Drizzle"
	case 61, 63, 65, 66, 67, 80, 81, 82:
		return "Rain"
	case 71, 73, 75, 77, 85, 86:
		return "Snow"
	case 95, 96, 99:
		return "Storm"
	default:
		return "Clouds"
	}
}
