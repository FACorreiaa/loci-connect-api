package localcontext

import "testing"

func TestNewWeatherAdapterFromEnv(t *testing.T) {
	tests := []struct {
		name           string
		provider       string
		openWeatherKey string
		wantType       any
		wantEstimated  bool
	}{
		{
			// The headline behaviour: a deployment that configures nothing at
			// all now gets real forecasts instead of a labelled placeholder.
			name:          "no configuration defaults to open-meteo",
			wantType:      &OpenMeteoAdapter{},
			wantEstimated: false,
		},
		{
			// An existing deployment that already pays for a key must not be
			// silently switched to a different provider by an upgrade.
			name:           "key present and no explicit provider keeps openweather",
			openWeatherKey: "abc123",
			wantType:       &OpenWeatherAdapter{},
			wantEstimated:  false,
		},
		{
			name:           "explicit openweather with a key",
			provider:       "openweather",
			openWeatherKey: "abc123",
			wantType:       &OpenWeatherAdapter{},
			wantEstimated:  false,
		},
		{
			// A real forecast from a keyless provider beats a placeholder, so a
			// misconfiguration degrades to open-meteo rather than to the stub.
			name:          "explicit openweather without a key falls back to open-meteo",
			provider:      "openweather",
			wantType:      &OpenMeteoAdapter{},
			wantEstimated: false,
		},
		{
			name:          "explicit stub is the only estimated path",
			provider:      "stub",
			wantType:      StubWeather{},
			wantEstimated: true,
		},
		{
			name:          "unknown provider falls back to open-meteo",
			provider:      "accuweather",
			wantType:      &OpenMeteoAdapter{},
			wantEstimated: false,
		},
		{
			name:          "provider is case and whitespace insensitive",
			provider:      "  OpenMeteo  ",
			wantType:      &OpenMeteoAdapter{},
			wantEstimated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEATHER_PROVIDER", tt.provider)
			t.Setenv("OPENWEATHER_API_KEY", tt.openWeatherKey)
			t.Setenv("OPENMETEO_BASE_URL", "")

			got, estimated := NewWeatherAdapterFromEnv(nil)

			switch tt.wantType.(type) {
			case *OpenMeteoAdapter:
				if _, ok := got.(*OpenMeteoAdapter); !ok {
					t.Fatalf("got %T, want *OpenMeteoAdapter", got)
				}
			case *OpenWeatherAdapter:
				if _, ok := got.(*OpenWeatherAdapter); !ok {
					t.Fatalf("got %T, want *OpenWeatherAdapter", got)
				}
			case StubWeather:
				if _, ok := got.(StubWeather); !ok {
					t.Fatalf("got %T, want StubWeather", got)
				}
			}

			if estimated != tt.wantEstimated {
				t.Errorf("estimated: got %v, want %v", estimated, tt.wantEstimated)
			}
		})
	}
}

// The base URL is configurable so a self-hosted Open-Meteo can be pointed at,
// and so nothing in a test suite reaches the public internet.
func TestNewWeatherAdapterFromEnv_HonoursOpenMeteoBaseURL(t *testing.T) {
	t.Setenv("WEATHER_PROVIDER", "openmeteo")
	t.Setenv("OPENWEATHER_API_KEY", "")
	t.Setenv("OPENMETEO_BASE_URL", "http://localhost:9999")

	got, _ := NewWeatherAdapterFromEnv(nil)
	a, ok := got.(*OpenMeteoAdapter)
	if !ok {
		t.Fatalf("got %T, want *OpenMeteoAdapter", got)
	}
	if a.baseURL != "http://localhost:9999" {
		t.Errorf("baseURL: got %q, want http://localhost:9999", a.baseURL)
	}
}

// An empty OPENMETEO_BASE_URL must not produce a request to "/v1/forecast".
func TestNewWeatherAdapterFromEnv_EmptyBaseURLUsesPublicEndpoint(t *testing.T) {
	t.Setenv("WEATHER_PROVIDER", "openmeteo")
	t.Setenv("OPENWEATHER_API_KEY", "")
	t.Setenv("OPENMETEO_BASE_URL", "")

	got, _ := NewWeatherAdapterFromEnv(nil)
	a := got.(*OpenMeteoAdapter)
	if a.baseURL != openMeteoBaseURL {
		t.Errorf("baseURL: got %q, want %q", a.baseURL, openMeteoBaseURL)
	}
}
