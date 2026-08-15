// Package travelhistory is the source of truth for where a traveller has
// actually been.
//
// It exists because two numbers in this codebase were not real: statistics'
// visited_cities_count was computed as `hotels + restaurants` and commented
// "Placeholder", and users.places_visited was a denormalised counter with no
// backing rows. Both are replaced by rows in user_visited_cities, each carrying
// the provenance that explains how it got there.
//
// The governing rule for everything in this package: when the evidence does not
// support a visit, record nothing. An absent city is honest; an inferred one is
// the bug we are removing.
package travelhistory

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is returned when a visit does not exist or is not owned by the caller.
	ErrNotFound = errors.New("visit not found")
	// ErrInvalidInput is returned when a visit cannot be placed on the globe.
	ErrInvalidInput = errors.New("invalid visit input")
)

// Source records how a visit came to be known. Values match the CHECK
// constraint on user_visited_cities.source.
type Source string

const (
	// SourceTrip means a saved trip had a day in this city with a real, past date.
	SourceTrip Source = "trip"
	// SourceVisitEvent means the traveller confirmed or rated a stop there.
	SourceVisitEvent Source = "visit_event"
	// SourceManual means the traveller entered it themselves.
	SourceManual Source = "manual"
	// SourceBackfill means the one-time pass over pre-existing signals wrote it.
	SourceBackfill Source = "backfill"
)

// Valid reports whether s is one of the persisted source values.
func (s Source) Valid() bool {
	switch s {
	case SourceTrip, SourceVisitEvent, SourceManual, SourceBackfill:
		return true
	default:
		return false
	}
}

// VisitedCity is one city the traveller has been to.
type VisitedCity struct {
	ID           uuid.UUID
	CityID       *uuid.UUID
	CityName     string
	Country      string
	CountryCode  *string
	Latitude     float64
	Longitude    float64
	Source       Source
	TripID       *uuid.UUID
	FirstVisitAt time.Time
	LastVisitAt  time.Time
	VisitCount   int32
}

// VisitedPOI is a single confirmed stop. Stored per-visit rather than rolled up
// so the city totals can be recomputed from evidence instead of trusted.
type VisitedPOI struct {
	ID        uuid.UUID
	POIID     string
	POIName   string
	CityName  string
	Latitude  *float64
	Longitude *float64
	TripID    *uuid.UUID
	Source    Source
	VisitedAt time.Time
}

// Summary backs the dashboard statistics rail.
//
// The Prev* fields are the same counts one period earlier. They exist so the
// rail can render a real delta; without them a trend arrow can only be invented.
type Summary struct {
	CitiesVisited    int32
	CountriesVisited int32
	POIsVisited      int32
	DistanceKm       float64
	TripsCompleted   int32
	FirstVisitAt     *time.Time
	LastVisitAt      *time.Time

	CitiesVisitedPrev    int32
	CountriesVisitedPrev int32
	POIsVisitedPrev      int32
	PeriodDays           int32
}

// GlobeArc is one leg between two placed points, ready to draw as a great
// circle. Sourced from real trip legs only — never synthesised between cities
// that merely happen to appear in the same trip.
type GlobeArc struct {
	FromName   string
	ToName     string
	FromLat    float64
	FromLon    float64
	ToLat      float64
	ToLon      float64
	DistanceKm float64
	TripID     *uuid.UUID
	Mode       string
	OccurredAt *time.Time
}

// VisitInput is a request to record one visit. POIID/POIName are optional: a
// city-level visit (from a trip day) has no single stop attached.
type VisitInput struct {
	CityID      *uuid.UUID
	CityName    string
	Country     string
	CountryCode *string
	Latitude    float64
	Longitude   float64
	Source      Source
	TripID      *uuid.UUID
	VisitedAt   time.Time
	POIID       string
	POIName     string
}

// DefaultPeriodDays is the comparison window used when a caller does not pick one.
const DefaultPeriodDays int32 = 365

// DefaultGlobeLimit caps how many cities the globe fetches in one call.
const DefaultGlobeLimit = 500

// Normalise trims the input and fills in defaults. It does not invent data:
// an unresolvable country stays empty rather than being guessed.
func (in *VisitInput) Normalise(now time.Time) {
	in.CityName = strings.TrimSpace(in.CityName)
	in.Country = strings.TrimSpace(in.Country)
	in.POIID = strings.TrimSpace(in.POIID)
	in.POIName = strings.TrimSpace(in.POIName)
	if !in.Source.Valid() {
		in.Source = SourceManual
	}
	if in.VisitedAt.IsZero() {
		in.VisitedAt = now
	}
	if in.CountryCode != nil {
		code := strings.ToUpper(strings.TrimSpace(*in.CountryCode))
		if len(code) != 2 {
			// A partial or malformed code is worse than none — it would render
			// as a wrong flag rather than as missing data.
			in.CountryCode = nil
		} else {
			in.CountryCode = &code
		}
	}
}

// Validate rejects a visit that cannot be placed on the globe. Coordinates are
// mandatory precisely so that nothing ends up silently pinned at 0,0.
func (in VisitInput) Validate() error {
	if in.CityName == "" {
		return ErrInvalidInput
	}
	if math.IsNaN(in.Latitude) || math.IsNaN(in.Longitude) {
		return ErrInvalidInput
	}
	if in.Latitude < -90 || in.Latitude > 90 {
		return ErrInvalidInput
	}
	if in.Longitude < -180 || in.Longitude > 180 {
		return ErrInvalidInput
	}
	// 0,0 is in the Gulf of Guinea. It is almost always a missing coordinate
	// rather than a real one, and letting it through would put a dot on the
	// globe for a place the traveller has never been.
	if in.Latitude == 0 && in.Longitude == 0 {
		return ErrInvalidInput
	}
	return nil
}

const earthRadiusKm = 6371.0088

// HaversineKm is the great-circle distance between two lng/lat points.
//
// Shares its definition with the client's arc renderer so the distance shown on
// a leg matches the curve drawn for it.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// TotalDistanceKm sums the great-circle hops between consecutive visits in the
// order given.
//
// This is "distance between the places you went, in the order you went", not a
// travel-distance estimate: it does not know about routes, layovers or return
// legs. Callers must sort by visit time before calling, and must present the
// result as such.
func TotalDistanceKm(cities []*VisitedCity) float64 {
	total := 0.0
	for i := 1; i < len(cities); i++ {
		prev, cur := cities[i-1], cities[i]
		if prev == nil || cur == nil {
			continue
		}
		total += HaversineKm(prev.Latitude, prev.Longitude, cur.Latitude, cur.Longitude)
	}
	return total
}

// Recorder lets other domains report a visit without importing this package's
// concrete types beyond the input struct.
//
// Recording is best-effort and must never fail the caller: a traveller marking a
// stop visited should not see an error because a history row could not be
// written. Same policy as preference.Recorder.
type Recorder interface {
	RecordVisit(ctx context.Context, userID uuid.UUID, in VisitInput)
}

// NopRecorder discards visits. Used when the domain is not wired, so callers
// never need a nil check.
type NopRecorder struct{}

// RecordVisit implements Recorder.
func (NopRecorder) RecordVisit(context.Context, uuid.UUID, VisitInput) {}
