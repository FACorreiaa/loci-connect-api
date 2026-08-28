package localcontext

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Weather provider names for WEATHER_PROVIDER.
const (
	WeatherProviderOpenMeteo   = "openmeteo"
	WeatherProviderOpenWeather = "openweather"
	WeatherProviderStub        = "stub"
)

// NewWeatherAdapterFromEnv picks the forecast source from the environment and
// reports whether the result is estimated (a placeholder the UI must label).
//
// Open-Meteo is the default because it needs no API key. That matters more than
// it sounds: the same adapter is injected into the local-context handler, the
// packing suggester and the compare service, so before this a deployment
// without OPENWEATHER_API_KEY showed a placeholder forecast in trip views, on
// /compare, inside the go-score and in packing advice — all at once.
//
//	WEATHER_PROVIDER=openmeteo    (default) no key needed
//	WEATHER_PROVIDER=openweather  requires OPENWEATHER_API_KEY
//	WEATHER_PROVIDER=stub         deterministic placeholder, for tests/offline
//
// Back-compatibility: when WEATHER_PROVIDER is unset but OPENWEATHER_API_KEY is
// present, OpenWeather is used. An existing deployment that configured a key
// keeps the provider it was already paying for, rather than being silently
// switched by an upgrade.
func NewWeatherAdapterFromEnv(logger *slog.Logger, cache *signalCache) (WeatherAdapter, bool) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("WEATHER_PROVIDER")))
	key := strings.TrimSpace(os.Getenv("OPENWEATHER_API_KEY"))

	if provider == "" {
		if key != "" {
			provider = WeatherProviderOpenWeather
		} else {
			provider = WeatherProviderOpenMeteo
		}
	}

	switch provider {
	case WeatherProviderStub:
		logf(logger, slog.LevelInfo, "weather: using stub forecast", slog.String("provider", provider))
		return StubWeather{}, true

	case WeatherProviderOpenWeather:
		if key == "" {
			// Falling back to Open-Meteo rather than the stub: a real forecast
			// from a keyless provider beats a labelled placeholder, and a
			// missing key is a config mistake we should not punish users for.
			logf(logger, slog.LevelWarn,
				"weather: WEATHER_PROVIDER=openweather but OPENWEATHER_API_KEY is empty; falling back to open-meteo")
			return NewOpenMeteoAdapter(os.Getenv("OPENMETEO_BASE_URL"), cache), false
		}
		logf(logger, slog.LevelInfo, "weather: using openweather", slog.String("provider", provider))
		return NewOpenWeatherAdapter(key), false

	case WeatherProviderOpenMeteo:
		logf(logger, slog.LevelInfo, "weather: using open-meteo (no API key required)")
		return NewOpenMeteoAdapter(os.Getenv("OPENMETEO_BASE_URL"), cache), false

	default:
		logf(logger, slog.LevelWarn,
			"weather: unknown WEATHER_PROVIDER, falling back to open-meteo",
			slog.String("provider", provider))
		return NewOpenMeteoAdapter(os.Getenv("OPENMETEO_BASE_URL"), cache), false
	}
}

// logf tolerates a nil logger so this is safe to call from wiring code and from
// tests that do not bother building one.
func logf(logger *slog.Logger, level slog.Level, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Log(context.Background(), level, msg, args...)
}
