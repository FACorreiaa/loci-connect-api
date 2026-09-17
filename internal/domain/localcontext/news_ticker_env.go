package localcontext

import (
	"log/slog"
	"os"
	"strings"
)

// NewNewsTickerFromEnv builds the ticker when FEEDS_BASE_URL is set, and
// returns nil otherwise — nil is a supported state the handler answers as
// "disabled". The three sources and the preference store are supplied by
// cmd/api, which owns the domain adapters.
//
//	FEEDS_BASE_URL   the shared feed aggregator, e.g.
//	                 http://feeds.horus.svc.cluster.local:8080
//
// No key: access is the allow-feeds NetworkPolicy.
func NewNewsTickerFromEnv(logger *slog.Logger, cache *signalCache, home HomeSource, next NextTripSource, visited VisitedSource, prefs NewsTickerPrefs) *NewsTickerService {
	base := strings.TrimSpace(os.Getenv("FEEDS_BASE_URL"))
	if base == "" {
		logf(logger, slog.LevelInfo, "news ticker: disabled (FEEDS_BASE_URL unset)")
		return nil
	}
	logf(logger, slog.LevelInfo, "news ticker: enabled", slog.String("base_url", base))
	return NewNewsTickerService(NewsTickerDeps{
		Client: NewSignalsHTTPClient(), BaseURL: base,
		Home: home, NextTrip: next, Visited: visited, Prefs: prefs, Cache: cache, Logger: logger,
	})
}
