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
	// AlertStrike has no producer. GDELT was built for it, measured and removed
	// (see deploy/OPS.md); transport disruption needs real transit APIs, which
	// are per-market. Kept because the proto value exists and cannot be
	// withdrawn, and because that is where such a source would land.
	AlertStrike AlertKind = "strike"

	// Kinds below have no proto enum value yet — they map to UNSPECIFIED on the
	// wire until the next proto release adds them. Declared here first because
	// the domain sources that produce them are useful before that lands, and an
	// alert a traveller can read beats silence.
	AlertHazard     AlertKind = "hazard"
	AlertAirQuality AlertKind = "air_quality"
	AlertTransit    AlertKind = "transit"
	AlertAdvisory   AlertKind = "advisory"
)

// Alert is a heads-up that may affect a trip day (closures/holidays/strikes).
type Alert struct {
	Kind   AlertKind  `json:"kind"`
	Title  string     `json:"title"`
	Detail string     `json:"detail"`
	Date   *time.Time `json:"date,omitempty"`

	// Severity grades how much this should count against the trip, 0..1.
	//
	// Zero means "unspecified" and is treated as full weight — see
	// effectiveSeverity. That default is load-bearing: it is what lets an Alert
	// built without a severity keep the flat-penalty behaviour the scorer had
	// before graded severity existed.
	Severity Severity `json:"severity,omitempty"`

	// Source names the provider this came from ("nager", "usgs", …). Carried so
	// a user can see who says so, and so a wrong or noisy feed can be
	// identified from the response rather than from the logs.
	Source string `json:"source,omitempty"`

	// Lat and Lon locate the alert when it has a place — a wildfire, a
	// cyclone, an earthquake. Nil for anything country-scoped: a public holiday
	// has no coordinates, and inventing some to make it mappable would be a
	// lie. Only located alerts can become map pins.
	//
	// Declared separately, not as `Lat, Lon *float64`, because a shared tag on
	// one line gives both fields the same JSON name and longitude silently
	// serialises as latitude.
	Lat *float64 `json:"lat,omitempty"`
	Lon *float64 `json:"lon,omitempty"`
}

// Located reports whether this alert can be drawn on a map.
func (a Alert) Located() bool { return a.Lat != nil && a.Lon != nil }

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
