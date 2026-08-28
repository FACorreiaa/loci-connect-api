package localcontext

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// NewSignalsHTTPClient builds the shared outbound client for every third-party
// data source, wired to the external-provider metrics.
//
// One client, not one per source: the per-host rate limiter inside it is what
// keeps us inside these free APIs' usage policies, and a client per source
// would defeat that by giving each its own budget for the same host.
func NewSignalsHTTPClient() *httpx.Client {
	cfg := httpx.Config{
		Timeout:       envDuration("SIGNALS_HTTP_TIMEOUT_SEC", 8*time.Second),
		MaxRetries:    envInt("SIGNALS_HTTP_MAX_RETRIES", 2),
		RatePerSecond: envFloat("SIGNALS_RATE_LIMIT_PER_SECOND", 5),
		UserAgent:     envString("SIGNALS_USER_AGENT", "loci/1.0 (+https://loci.app)"),
	}
	cfg.Burst = envInt("SIGNALS_RATE_LIMIT_BURST", 5)

	return httpx.New(cfg).WithObserver(func(source, outcome string, d time.Duration) {
		observability.ExternalRequestsTotal.WithLabelValues(source, outcome).Inc()
		observability.ExternalRequestDuration.WithLabelValues(source).Observe(d.Seconds())
	})
}

// NewGathererFromEnv assembles the live alert sources.
//
// Returns nil when signals are switched off, which every caller treats as "no
// alerts" rather than as an error — Gatherer's methods are nil-safe for exactly
// this reason.
//
//	SIGNALS_ENABLED=false     disables every source
//	NAGER_DATE_BASE_URL       override the holiday API (self-host or test)
//	BIGDATACLOUD_BASE_URL     override the reverse geocoder
//
// Everything works with no keys and no configuration at all.
func NewGathererFromEnv(logger *slog.Logger, cache *signalCache) *Gatherer {
	if !envBool("SIGNALS_ENABLED", true) {
		logf(logger, slog.LevelInfo, "signals: disabled by SIGNALS_ENABLED")
		return nil
	}

	client := NewSignalsHTTPClient()

	// Holidays are country-scoped, so they need the geocoder to turn the
	// requested coordinates into a country.
	geocoder := NewBigDataCloudGeocoder(os.Getenv("BIGDATACLOUD_BASE_URL"), client, cache)

	radiusKm := envFloat("HAZARD_RADIUS_KM", defaultHazardRadiusKm)

	sources := []SignalSource{
		NewHolidaySource(os.Getenv("NAGER_DATE_BASE_URL"), client, cache),
	}
	// GDACS is the multi-hazard source (wildfire, cyclone, flood, quake,
	// volcano) and carries its own severity triage; USGS adds locally
	// significant quakes GDACS does not rank globally. Each has its own switch
	// because they fail independently and GDACS is the noisier of the two.
	if envBool("GDACS_ENABLED", true) {
		sources = append(sources, NewGDACSSource(os.Getenv("GDACS_BASE_URL"), client, radiusKm, cache))
	}
	if envBool("USGS_ENABLED", true) {
		sources = append(sources, NewUSGSSource(os.Getenv("USGS_BASE_URL"), client, radiusKm, cache))
	}
	// Air quality always has a value, unlike the event-driven sources, so the
	// threshold is what stops it attaching an alert to every trip forever.
	if envBool("AIR_QUALITY_ENABLED", true) {
		sources = append(sources, NewAirQualitySource(
			os.Getenv("OPENMETEO_AIR_QUALITY_BASE_URL"),
			os.Getenv("OPENMETEO_API_KEY"),
			client,
			envFloat("AIR_QUALITY_ALERT_THRESHOLD", defaultAirQualityThreshold),
			cache,
		))
	}

	names := make([]string, 0, len(sources))
	for _, s := range sources {
		names = append(names, s.Name())
	}
	logf(logger, slog.LevelInfo, "signals: enabled", slog.String("sources", strings.Join(names, ",")))

	return NewGatherer(geocoder, logger, sources...)
}

func envString(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func envFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return f
}

func envDuration(key string, def time.Duration) time.Duration {
	n := envInt(key, int(def.Seconds()))
	return time.Duration(n) * time.Second
}

// NewFXFromEnv builds the exchange-rate adapter and the fuel assumptions used
// to price a driving leg.
//
// Returns a nil adapter when FX is switched off, which the handler treats as
// "rates are not offered" rather than as an error.
//
//	FX_ENABLED=false             disables rate lookups
//	FX_BASE_CURRENCY=EUR         the traveller's home currency
//	FUEL_LITRES_PER_100KM=6.5    drive-cost assumptions; always stated in the
//	FUEL_PRICE_PER_LITRE=1.75    response so a user can correct them
func NewFXFromEnv(client *httpx.Client, cache *signalCache) (adapter *FXAdapter, base string, litresPer100Km, pricePerLitre float64) {
	base = envString("FX_BASE_CURRENCY", "EUR")
	litresPer100Km = envFloat("FUEL_LITRES_PER_100KM", defaultLitresPer100Km)
	pricePerLitre = envFloat("FUEL_PRICE_PER_LITRE", defaultPricePerLitre)

	if !envBool("FX_ENABLED", true) {
		return nil, base, litresPer100Km, pricePerLitre
	}
	return NewFXAdapter(os.Getenv("FX_BASE_URL"), client, cache), base, litresPer100Km, pricePerLitre
}

// NewSignalCacheFromEnv wraps the app's shared cache store for provider payloads.
// A nil store is supported and simply disables caching.
func NewSignalCacheFromEnv(store cachestore.Store, logger *slog.Logger) *signalCache {
	return newSignalCache(store, logger)
}
