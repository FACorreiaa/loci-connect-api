package compare

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

const (
	// discoverTimeout bounds one generation. It runs detached, so nothing is
	// waiting on it; this only stops a wedged provider holding a goroutine open
	// forever.
	discoverTimeout = 90 * time.Second

	// maxDiscoveriesPerRequest caps how many cities one comparison may kick off
	// work for. A Pro user comparing eight cities we have never seen would
	// otherwise fire eight LLM generations from a single page load. The
	// remaining cities pick theirs up on a later comparison.
	maxDiscoveriesPerRequest = 2

	// discoverCooldown stops a city whose generation legitimately produced
	// nothing from being retried on every page load. Per replica, which is fine
	// for a cost guard.
	discoverCooldown = time.Hour
)

// POIDiscoverer fills in a city that has no places stored yet.
type POIDiscoverer interface {
	DiscoverPOIsForCity(ctx context.Context, cityID uuid.UUID, cityName string) error
}

// discoveryState is the per-service bookkeeping that keeps discovery cheap.
type discoveryState struct {
	group    singleflight.Group
	mu       sync.Mutex
	lastTry  map[uuid.UUID]time.Time
	inFlight map[uuid.UUID]struct{}
}

// WithDiscovery lets compare fill in cities that have no places yet.
//
// Optional, and attached as a builder rather than a tenth constructor argument
// for the same reason WithSignals is: comparison worked without it and must keep
// working when it is switched off.
func (s *Service) WithDiscovery(d POIDiscoverer) *Service {
	s.discoverer = d
	s.discovery = &discoveryState{
		lastTry:  map[uuid.UUID]time.Time{},
		inFlight: map[uuid.UUID]struct{}{},
	}
	return s
}

// maybeDiscover starts a background generation for a city with no places.
//
// It deliberately does not block. Generation is an LLM round-trip measured in
// seconds, a comparison fans out over up to eight cities, and the column is
// perfectly answerable without it: weather, distance, drive time and the go-score
// are all computed from the city's coordinates, and buildProsCons already says
// when POI data is thin. Waiting would trade a complete answer now for a slightly
// better answer much later, on a page whose whole point is a quick verdict.
//
// Returns whether it started work, so the caller can honour the per-request cap.
func (s *Service) maybeDiscover(cityID uuid.UUID, cityName string) bool {
	if s.discoverer == nil || s.discovery == nil || cityID == uuid.Nil || cityName == "" {
		return false
	}
	if !s.discovery.claim(cityID) {
		return false
	}

	concurrency.Run(s.logger, func() {
		// The request context dies with the RPC, and this outlives it by
		// design, so it gets a fresh deadline of its own.
		ctx, cancel := context.WithTimeout(context.Background(), discoverTimeout)
		defer cancel()
		defer s.discovery.release(cityID)

		// Keyed on the city so two people comparing the same place, or one
		// person re-running, cost one generation rather than two.
		_, err, shared := s.discovery.group.Do(cityID.String(), func() (any, error) {
			return nil, s.discoverer.DiscoverPOIsForCity(ctx, cityID, cityName)
		})
		if err != nil {
			s.logger.WarnContext(ctx, "could not discover places for a newly placed city",
				slog.String("city", cityName),
				slog.String("city_id", cityID.String()),
				slog.Any("error", err))
			return
		}
		s.logger.InfoContext(ctx, "discovered places for a city that had none",
			slog.String("city", cityName),
			slog.String("city_id", cityID.String()),
			slog.Bool("shared", shared))
	})
	return true
}

// claim reports whether this city is free to work on, and marks it taken.
func (d *discoveryState) claim(cityID uuid.UUID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, busy := d.inFlight[cityID]; busy {
		return false
	}
	if last, ok := d.lastTry[cityID]; ok && time.Since(last) < discoverCooldown {
		return false
	}
	d.inFlight[cityID] = struct{}{}
	d.lastTry[cityID] = time.Now()
	return true
}

func (d *discoveryState) release(cityID uuid.UUID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inFlight, cityID)
}
