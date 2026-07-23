// Package localcontext provides resilient provider adapters for trip-time local
// context: weather, booking, and transport. Weather has a real OpenWeather
// implementation; booking/transport are stubs behind the same interfaces so the
// rest of the app can integrate now and light up real providers later (Slice 4).
package localcontext

import (
	"context"
	"time"
)

// WeatherDay is a one-day forecast summary.
type WeatherDay struct {
	Date       time.Time `json:"date"`
	HighC      float64   `json:"high_c"`
	LowC       float64   `json:"low_c"`
	Condition  string    `json:"condition"` // e.g. "Clear", "Rain", "Clouds"
	PrecipProb float64   `json:"precip_prob"`
}

// WeatherAdapter returns a short daily forecast for a location.
type WeatherAdapter interface {
	Forecast(ctx context.Context, lat, lon float64, days int) ([]WeatherDay, error)
}

// AlertKind classifies a local-context alert.
type AlertKind string

const (
	AlertClosure AlertKind = "closure"
	AlertHoliday AlertKind = "holiday"
	AlertStrike  AlertKind = "strike"
)

// Alert is a heads-up that may affect a trip day (closures/holidays/strikes).
type Alert struct {
	Kind   AlertKind  `json:"kind"`
	Title  string     `json:"title"`
	Detail string     `json:"detail"`
	Date   *time.Time `json:"date,omitempty"`
}

// BookingOption is a placeholder booking result (real providers land later).
type BookingOption struct {
	Provider string  `json:"provider"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

// TransportOption is a placeholder transport result.
type TransportOption struct {
	Mode         string `json:"mode"` // "transit" | "taxi" | "walk"
	Summary      string `json:"summary"`
	DurationMins int    `json:"duration_mins"`
}

// BookingAdapter surfaces booking options for a place.
type BookingAdapter interface {
	Options(ctx context.Context, poiID string) ([]BookingOption, error)
}

// TransportAdapter surfaces transport options between two points.
type TransportAdapter interface {
	Options(ctx context.Context, fromLat, fromLon, toLat, toLon float64) ([]TransportOption, error)
}
