package localcontext

import (
	"context"
	"math"
	"time"
)

// StubWeather is a deterministic fallback used when no OpenWeather key is
// configured, so trip views still render a (clearly placeholder) forecast.
type StubWeather struct{}

func (StubWeather) Forecast(_ context.Context, _, _ float64, days int) ([]WeatherDay, error) {
	if days <= 0 {
		days = 5
	}
	base := time.Now().UTC().Truncate(24 * time.Hour)
	out := make([]WeatherDay, 0, days)
	for i := 0; i < days; i++ {
		out = append(out, WeatherDay{
			Date:       base.AddDate(0, 0, i),
			HighC:      22,
			LowC:       14,
			Condition:  "Clear",
			PrecipProb: 0.1,
		})
	}
	return out, nil
}

// StubBooking returns placeholder booking options behind the real interface.
type StubBooking struct{}

func (StubBooking) Options(_ context.Context, _ string) ([]BookingOption, error) {
	return []BookingOption{}, nil
}

// StubTransport returns a simple walking estimate as a placeholder.
type StubTransport struct{}

func (StubTransport) Options(_ context.Context, fromLat, fromLon, toLat, toLon float64) ([]TransportOption, error) {
	// Rough straight-line walking estimate (~5 km/h) so the field isn't empty.
	km := haversineKm(fromLat, fromLon, toLat, toLon)
	return []TransportOption{{
		Mode:         "walk",
		Summary:      "Estimated walking time",
		DurationMins: int(km / 5.0 * 60.0),
	}}, nil
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180.0 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	sinLat := math.Sin(dLat / 2)
	sinLon := math.Sin(dLon / 2)
	a := sinLat*sinLat + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*sinLon*sinLon
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}
