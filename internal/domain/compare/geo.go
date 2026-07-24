package compare

import "math"

// HaversineKm returns the great-circle distance in kilometers.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180.0 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	sinLat := math.Sin(dLat / 2)
	sinLon := math.Sin(dLon / 2)
	a := sinLat*sinLat + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*sinLon*sinLon
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// DriveMins estimates road travel time at ~80 km/h average.
func DriveMins(distanceKm float64) int {
	if distanceKm <= 0 {
		return 0
	}
	return int(distanceKm / 80.0 * 60.0)
}
