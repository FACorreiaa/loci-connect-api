package localcontext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const fakeForecast = `{"list":[
  {"dt":1000,"main":{"temp_max":20,"temp_min":12},"weather":[{"main":"Clear"}],"pop":0.1},
  {"dt":11000,"main":{"temp_max":24,"temp_min":10},"weather":[{"main":"Clouds"}],"pop":0.2},
  {"dt":90000,"main":{"temp_max":18,"temp_min":9},"weather":[{"main":"Rain"}],"pop":0.8}
]}`

func TestOpenWeatherAggregatesAndCaches(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Query().Get("appid") == "" {
			t.Error("missing appid")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakeForecast))
	}))
	defer srv.Close()

	a := NewOpenWeatherAdapter("test-key")
	a.baseURL = srv.URL

	days, err := a.Forecast(context.Background(), 38.7, -9.1, 5)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	// dt 1000 & 11000 -> same day (1970-01-01); dt 90000 -> next day.
	if len(days) != 2 {
		t.Fatalf("expected 2 days, got %d", len(days))
	}
	// Day 1 aggregates: high=max(20,24)=24, low=min(12,10)=10, pop=max(.1,.2)=.2.
	if days[0].HighC != 24 || days[0].LowC != 10 || days[0].PrecipProb != 0.2 {
		t.Fatalf("bad day-1 aggregation: %+v", days[0])
	}
	if days[1].Condition != "Rain" || days[1].PrecipProb != 0.8 {
		t.Fatalf("bad day-2: %+v", days[1])
	}

	// Second call within TTL -> served from cache, no extra HTTP hit.
	if _, err := a.Forecast(context.Background(), 38.7, -9.1, 5); err != nil {
		t.Fatalf("forecast 2: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 HTTP hit (cache), got %d", got)
	}
}

func TestOpenWeatherRequiresKey(t *testing.T) {
	a := NewOpenWeatherAdapter("")
	if _, err := a.Forecast(context.Background(), 1, 1, 3); err == nil {
		t.Fatal("expected error without API key")
	}
}

func TestStubWeather(t *testing.T) {
	days, err := StubWeather{}.Forecast(context.Background(), 0, 0, 3)
	if err != nil || len(days) != 3 {
		t.Fatalf("stub weather: %v len=%d", err, len(days))
	}
}

func TestStubTransportWalk(t *testing.T) {
	// ~1.11km apart (0.01 deg latitude) -> a non-zero walking estimate.
	opts, err := StubTransport{}.Options(context.Background(), 38.70, -9.10, 38.71, -9.10)
	if err != nil || len(opts) != 1 || opts[0].Mode != "walk" || opts[0].DurationMins <= 0 {
		t.Fatalf("stub transport: %v %+v", err, opts)
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	a := NewOpenWeatherAdapter("k")
	a.cache["38.70,-9.10"] = cacheEntry{days: []WeatherDay{{Condition: "Old"}}, cachedAt: time.Unix(0, 0)}
	a.now = func() time.Time { return time.Unix(0, 0).Add(a.ttl + time.Minute) }
	// Stale entry -> would try network (no server) -> error, proving no stale serve.
	if _, err := a.Forecast(context.Background(), 38.70, -9.10, 3); err == nil {
		t.Fatal("expected network attempt on expired cache")
	}
}
