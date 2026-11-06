// Package routing computes walking (or rolling) legs between two points for
// travellers with access needs.
//
// The interface is named for its role, not a vendor: PedestrianRouter is
// satisfied by the OpenRouteService client in this package today, and a
// self-hosted engine later would be configuration, not a new call site.
package routing

import (
	"context"
	"errors"
	"sync"
	"time"
)

// LatLon is a WGS84 coordinate.
type LatLon struct {
	Lat float64
	Lon float64
}

// Profile is the kind of traveller a route is computed for.
type Profile string

const (
	// ProfileWalking is ordinary pedestrian routing.
	ProfileWalking Profile = "foot-walking"
	// ProfileWheelchair routes for wheels: it can avoid steps, steep slopes,
	// high kerbs and rough surfaces where the map knows about them.
	ProfileWheelchair Profile = "wheelchair"
)

// Options shapes one route. Zero values mean "the router's default".
type Options struct {
	Profile Profile
	// MaxInclinePct is the steepest slope allowed, in percent (wheelchair
	// profile only).
	MaxInclinePct int
	// MaxSlopedKerbM is the highest kerb allowed, in metres (wheelchair only).
	MaxSlopedKerbM float64
	// AvoidSteps excludes steps where the router supports it.
	AvoidSteps bool
	// PavedOnly excludes cobbles and rough surfaces (wheelchair only).
	PavedOnly bool
}

// Leg is one computed route between two points.
type Leg struct {
	DistanceM float64 `json:"distance_m"`
	DurationS float64 `json:"duration_s"`
	AscentM   float64 `json:"ascent_m"`
	// Warnings are the router's own caveats about this route, in its words.
	Warnings []string `json:"warnings,omitempty"`
}

// PedestrianRouter computes a leg between two points.
type PedestrianRouter interface {
	Route(ctx context.Context, from, to LatLon, opts Options) (Leg, error)
}

var (
	// ErrNoRoute means the router looked and found no route that satisfies the
	// options — for a wheelchair, typically "only via steps". It is an answer,
	// not an outage: callers should tell the traveller, not fall back silently.
	ErrNoRoute = errors.New("routing: no route satisfies the options")
	// ErrUnavailable means the router could not answer (outage, rate limit,
	// exhausted daily budget). Callers fall back to an estimate.
	ErrUnavailable = errors.New("routing: router unavailable")
)

// DailyBudget caps calls to a metered router per UTC day.
//
// It counts per process. Loci runs one API replica, so this is the whole
// budget; with more replicas each would get its own, and the provider's own
// 429 (which the router reports as ErrUnavailable) is the backstop.
type DailyBudget struct {
	mu    sync.Mutex
	limit int
	day   string
	used  int
	now   func() time.Time
}

// NewDailyBudget allows limit calls per UTC day. A limit of zero or less means
// no limit.
func NewDailyBudget(limit int) *DailyBudget {
	return &DailyBudget{limit: limit, now: time.Now}
}

// Take spends one call, reporting false when today's budget is gone.
func (b *DailyBudget) Take() bool {
	if b == nil || b.limit <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	today := b.now().UTC().Format(time.DateOnly)
	if today != b.day {
		b.day, b.used = today, 0
	}
	if b.used >= b.limit {
		return false
	}
	b.used++
	return true
}
