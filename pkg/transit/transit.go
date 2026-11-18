// Package transit says what public transport is near a point, and builds the
// links that open a journey in a maps app.
//
// It knows where stops are and which lines the map says call there. It knows
// nothing about timetables: nothing here says when anything runs, and callers
// must not word its answers as if it did.
//
// The stop data comes from OpenStreetMap through a server that speaks the
// Overpass query language. Which server is configuration: the public ones ask
// commercial and automated users to run their own, so production points this
// at an instance Loci runs or pays for.
package transit

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
)

// Attribution is the credit OpenStreetMap's licence asks for wherever its
// data is shown.
const Attribution = "Map data © OpenStreetMap contributors"

// ErrUnavailable means the map server could not answer (outage, busy, daily
// budget spent, or none configured). Callers show what they can without it.
var ErrUnavailable = errors.New("transit: map data unavailable")

// Mode is the kind of public transport a stop serves.
type Mode int

const (
	ModeUnspecified Mode = iota
	ModeMetro
	ModeTrain
	ModeTram
	ModeBus
	ModeFerry
)

// Stop is one public transport stop.
type Stop struct {
	Name string `json:"name"`
	Mode Mode   `json:"mode"`
	// Lines are the line names the map has for the stop ("L3", "V17"), sorted.
	// Empty when the map has none, which is common and not an error.
	Lines     []string `json:"lines,omitempty"`
	Lat       float64  `json:"lat"`
	Lon       float64  `json:"lon"`
	DistanceM int      `json:"distance_m"`
}

// Highlight counts one kind of everyday place around a point.
type Highlight struct {
	// Kind is "eat", "bars", "groceries" or "pharmacies".
	Kind        string `json:"kind"`
	Count       int    `json:"count"`
	NearestName string `json:"nearest_name,omitempty"`
	NearestM    int    `json:"nearest_m"`
}

// Surroundings is what is around one point.
type Surroundings struct {
	Stops      []Stop      `json:"stops"`
	Highlights []Highlight `json:"highlights"`
	FetchedAt  time.Time   `json:"fetched_at"`
}

// Place is somewhere found on the map by name or address.
type Place struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Address string  `json:"address,omitempty"`
	// Stars is the map's star tag as written, e.g. "4"; often empty.
	Stars string `json:"stars,omitempty"`
}

// LatLon is a WGS84 coordinate.
type LatLon struct {
	Lat float64
	Lon float64
}

// DistanceM is the straight-line distance between two points, in metres.
func DistanceM(a, b LatLon) int {
	return int(math.Round(geo.HaversineKm(a.Lat, a.Lon, b.Lat, b.Lon) * 1000))
}

const (
	// A street route is longer than the straight line between its ends; 1.3
	// is the usual city-grid allowance.
	detourFactor = 1.3
	// 80 m a minute is 4.8 km/h, an unhurried walk.
	walkMPerMin = 80.0
)

// EstimateWalkMinutes turns a straight-line distance into walking minutes.
// It is an estimate, not a routed walk: say so wherever it is shown.
func EstimateWalkMinutes(straightLineM int) int {
	if straightLineM <= 0 {
		return 1
	}
	return max(1, int(math.Ceil(float64(straightLineM)*detourFactor/walkMPerMin)))
}

// WalkableM is how far a traveller is offered a walk before public transport
// is suggested instead: about 25 minutes.
const WalkableM = 1500

// SuggestedMode is "walk" for a distance most people would walk, else
// "transit".
func SuggestedMode(straightLineM int) string {
	if straightLineM <= WalkableM {
		return "walk"
	}
	return "transit"
}

func coord(p LatLon) string { return fmt.Sprintf("%.5f,%.5f", p.Lat, p.Lon) }

// GoogleTransitURL opens Google Maps directions between two points in public
// transport mode.
func GoogleTransitURL(from, to LatLon) string {
	q := url.Values{}
	q.Set("api", "1")
	q.Set("origin", coord(from))
	q.Set("destination", coord(to))
	q.Set("travelmode", "transit")
	return "https://www.google.com/maps/dir/?" + q.Encode()
}

// AppleTransitURL opens Apple Maps directions between two points in public
// transport mode.
func AppleTransitURL(from, to LatLon) string {
	q := url.Values{}
	q.Set("saddr", coord(from))
	q.Set("daddr", coord(to))
	q.Set("dirflg", "r")
	return "https://maps.apple.com/?" + q.Encode()
}
