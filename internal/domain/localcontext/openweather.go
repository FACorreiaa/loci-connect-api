package localcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

const openWeatherBaseURL = "https://api.openweathermap.org/data/2.5/forecast"

// OpenWeatherAdapter is a real WeatherAdapter backed by OpenWeather's free
// 5-day/3-hour forecast, aggregated to daily summaries. It applies an HTTP
// timeout and a short in-memory cache so trip views don't hammer the API or
// block the stream on a slow provider.
type OpenWeatherAdapter struct {
	apiKey  string
	baseURL string
	http    *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
	ttl   time.Duration
	now   func() time.Time
}

type cacheEntry struct {
	days     []WeatherDay
	cachedAt time.Time
}

// NewOpenWeatherAdapter builds an adapter with sane resilience defaults.
func NewOpenWeatherAdapter(apiKey string) *OpenWeatherAdapter {
	return &OpenWeatherAdapter{
		apiKey:  apiKey,
		baseURL: openWeatherBaseURL,
		http:    &http.Client{Timeout: 8 * time.Second},
		cache:   make(map[string]cacheEntry),
		ttl:     30 * time.Minute,
		now:     time.Now,
	}
}

func (a *OpenWeatherAdapter) cacheKey(lat, lon float64) string {
	// Round to ~1km so nearby requests share a cache entry.
	return fmt.Sprintf("%.2f,%.2f", lat, lon)
}

func (a *OpenWeatherAdapter) Forecast(ctx context.Context, lat, lon float64, days int) ([]WeatherDay, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("openweather: missing API key")
	}
	if days <= 0 {
		days = 5
	}

	key := a.cacheKey(lat, lon)
	a.mu.Lock()
	if e, ok := a.cache[key]; ok && a.now().Sub(e.cachedAt) < a.ttl {
		days := clampDays(e.days, days)
		a.mu.Unlock()
		return days, nil
	}
	a.mu.Unlock()

	q := url.Values{}
	q.Set("lat", fmt.Sprintf("%f", lat))
	q.Set("lon", fmt.Sprintf("%f", lon))
	q.Set("appid", a.apiKey)
	q.Set("units", "metric")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openweather request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openweather status %d", resp.StatusCode)
	}

	var body openWeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("openweather decode: %w", err)
	}

	daily := aggregateDaily(body.List)

	a.mu.Lock()
	a.cache[key] = cacheEntry{days: daily, cachedAt: a.now()}
	a.mu.Unlock()

	return clampDays(daily, days), nil
}

func clampDays(days []WeatherDay, n int) []WeatherDay {
	if n < len(days) {
		return days[:n]
	}
	return days
}

// --- OpenWeather wire types ---

type openWeatherResponse struct {
	List []openWeatherEntry `json:"list"`
}

type openWeatherEntry struct {
	Dt   int64 `json:"dt"`
	Main struct {
		TempMax float64 `json:"temp_max"`
		TempMin float64 `json:"temp_min"`
	} `json:"main"`
	Weather []struct {
		Main string `json:"main"`
	} `json:"weather"`
	Pop float64 `json:"pop"`
}

// aggregateDaily folds 3-hour entries into per-day summaries (max high, min low,
// most frequent condition, max precip probability), ordered by date.
func aggregateDaily(entries []openWeatherEntry) []WeatherDay {
	type acc struct {
		high, low  float64
		pop        float64
		conditions map[string]int
		set        bool
	}
	byDay := map[string]*acc{}
	var order []string

	for _, e := range entries {
		day := time.Unix(e.Dt, 0).UTC().Format("2006-01-02")
		a := byDay[day]
		if a == nil {
			a = &acc{high: e.Main.TempMax, low: e.Main.TempMin, conditions: map[string]int{}}
			byDay[day] = a
			order = append(order, day)
		}
		if e.Main.TempMax > a.high {
			a.high = e.Main.TempMax
		}
		if e.Main.TempMin < a.low {
			a.low = e.Main.TempMin
		}
		if e.Pop > a.pop {
			a.pop = e.Pop
		}
		if len(e.Weather) > 0 {
			a.conditions[e.Weather[0].Main]++
		}
	}

	sort.Strings(order)
	out := make([]WeatherDay, 0, len(order))
	for _, day := range order {
		a := byDay[day]
		d, _ := time.Parse("2006-01-02", day)
		out = append(out, WeatherDay{
			Date:       d,
			HighC:      a.high,
			LowC:       a.low,
			Condition:  topCondition(a.conditions),
			PrecipProb: a.pop,
		})
	}
	return out
}

func topCondition(m map[string]int) string {
	best, bestN := "", -1
	for c, n := range m {
		if n > bestN {
			best, bestN = c, n
		}
	}
	return best
}
