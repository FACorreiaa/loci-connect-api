package localcontext

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// signalCache is the shared cache for third-party provider payloads.
//
// It replaces the per-adapter map+mutex each source used to carry. Two reasons
// that mattered: those caches died with the process, so a restart re-fetched
// everything from providers whose free tiers we are trying not to exhaust; and
// they were per-instance, so scaling out multiplied the request rate by the
// replica count.
//
// Backed by cachestore.TieredStore, which keeps everything in memory and
// mirrors to Redis when REDIS_URL is set. Redis only mirrors *string* values,
// which is why every payload is JSON-encoded rather than stored as a struct —
// storing a struct would silently degrade to memory-only.
type signalCache struct {
	store  cachestore.Store
	logger *slog.Logger
}

// newSignalCache wraps a store. A nil store disables caching entirely, which is
// a supported state: every adapter still works, it just talks to its provider
// every time.
func newSignalCache(store cachestore.Store, logger *slog.Logger) *signalCache {
	return &signalCache{store: store, logger: logger}
}

// cacheGet returns a decoded payload and whether it was a hit.
//
// A free function rather than a method because Go does not allow type
// parameters on methods.
func cacheGet[T any](c *signalCache, source, key string) (T, bool) {
	var out T
	if c == nil || c.store == nil {
		return out, false
	}

	raw, ok := c.store.Get(cacheKeyFor(source, key))
	if !ok {
		return out, false
	}
	encoded, ok := raw.(string)
	if !ok {
		return out, false
	}
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		// A payload we cannot decode is a payload we cannot use — most likely
		// this key's shape changed across a deploy. Treat it as a miss and let
		// the next write overwrite it.
		return out, false
	}

	observability.ExternalCacheHitsTotal.WithLabelValues(source).Inc()
	return out, true
}

// cacheSet stores a payload under a per-source TTL.
func cacheSet[T any](c *signalCache, source, key string, value T, ttl time.Duration) {
	if c == nil || c.store == nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		if c.logger != nil {
			c.logger.Warn("signals: could not cache payload",
				slog.String("source", source), slog.Any("error", err))
		}
		return
	}
	c.store.Set(cacheKeyFor(source, key), string(encoded), ttl)
}

// cacheKeyFor namespaces by source so two providers cannot collide on a
// coordinate key that means different things to each.
func cacheKeyFor(source, key string) string {
	return "signal:" + source + ":" + key
}

// Per-source TTLs.
//
// Each is the interval at which the upstream data can actually change, not a
// uniform guess: re-fetching a year's public holidays every half hour spends a
// free tier's quota on a constant, and caching earthquakes for a day would miss
// the ones that matter.
const (
	ttlForecast   = 30 * time.Minute // matches the existing OpenWeather TTL
	ttlAirQuality = 30 * time.Minute // hourly upstream
	ttlQuakes     = 10 * time.Minute // genuinely fast-moving
	ttlHazards    = 30 * time.Minute // GDACS moves slowly
	ttlHolidays   = 30 * 24 * time.Hour
	ttlGeocode    = 30 * 24 * time.Hour // the country of a coordinate does not change
	ttlFx         = 12 * time.Hour      // the ECB publishes once a working day
	ttlNews       = 20 * time.Minute    // 15-minute upstream cadence
)
