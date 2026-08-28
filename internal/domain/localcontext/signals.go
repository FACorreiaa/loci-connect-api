package localcontext

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

// Source names. These are the `source` label on every metric and the value
// carried on an Alert, so they are part of the observable contract — renaming
// one breaks a dashboard.
const (
	SourceGeocode    = "bigdatacloud"
	SourceHolidays   = "nager"
	SourceOpenMeteo  = "open-meteo"
	SourceGDACS      = "gdacs"
	SourceAirQuality = "open-meteo-air"
	SourceNews       = "gdelt"
	SourceUSGS       = "usgs"
)

// Severity grades how much an alert should count against a trip.
//
// It exists because the scorer used to charge a flat penalty per alert, which
// was fine while nothing produced alerts but stops being fine the moment real
// feeds do: a public holiday and a wildfire are not the same news, and a
// destination with four minor notices is not automatically worse than one with
// a single serious one.
//
// The zero value means "unspecified" and is treated as full weight, so an alert
// built without thinking about severity behaves exactly as it did before.
type Severity float64

const (
	SeverityMinor    Severity = 0.25
	SeverityModerate Severity = 0.5
	SeverityMajor    Severity = 1.0
)

// SignalRequest is what every source is asked.
type SignalRequest struct {
	Lat, Lon float64
	// CountryCode is ISO-3166-1 alpha-2, or empty when it could not be
	// resolved. Country-scoped sources must return no alerts rather than an
	// error when it is empty.
	CountryCode string
	// Start and End bound the trip window. Sources filter to it so a holiday
	// three months away does not affect this weekend's score.
	Start, End time.Time
}

// SignalSource is one provider of trip-time alerts.
//
// Implementations must be safe for concurrent use and must not treat "nothing
// happening" as an error — an empty slice is the common, correct answer.
type SignalSource interface {
	// Name is the source label used in logs and metrics.
	Name() string
	Fetch(ctx context.Context, req SignalRequest) ([]Alert, error)
}

// Gatherer fans out to every configured source and merges the results.
//
// The contract that matters is degradation: a source that fails is logged and
// skipped, never propagated. Trip context is a nicety layered on top of the
// itinerary, and the existing handler convention is that a third-party hiccup
// must not fail the RPC or blank the trip view.
type Gatherer struct {
	sources []SignalSource
	country CountryResolver
	logger  *slog.Logger
	// timeout bounds the whole fan-out. Sources run concurrently, so this is
	// the slowest source's budget, not their sum.
	timeout time.Duration
}

// NewGatherer builds a Gatherer. A nil CountryResolver is supported: country-
// scoped sources then simply see an empty country code and return nothing.
func NewGatherer(country CountryResolver, logger *slog.Logger, sources ...SignalSource) *Gatherer {
	return &Gatherer{
		sources: sources,
		country: country,
		logger:  logger,
		timeout: 10 * time.Second,
	}
}

// Enabled reports whether there is anything to gather. Callers use it to skip
// the country lookup entirely when no sources are configured.
func (g *Gatherer) Enabled() bool {
	return g != nil && len(g.sources) > 0
}

// Gather collects alerts for a location and window. It never returns an error:
// the worst case is an empty slice, which is indistinguishable to a caller from
// a destination where nothing is happening — which is exactly why the per-source
// failures are counted in loci_external_requests_total rather than swallowed
// silently.
func (g *Gatherer) Gather(ctx context.Context, lat, lon float64, start, end time.Time) []Alert {
	if !g.Enabled() {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	req := SignalRequest{Lat: lat, Lon: lon, Start: start, End: end}

	// Resolved once and shared: every country-scoped source would otherwise
	// repeat the same lookup.
	if g.country != nil {
		code, err := g.country.CountryCode(ctx, lat, lon)
		if err != nil {
			g.logf(ctx, slog.LevelWarn, "signals: country lookup failed; country-scoped sources will be skipped",
				slog.Any("error", err))
		} else {
			req.CountryCode = code
		}
	}

	var (
		mu  sync.Mutex
		out []Alert
		wg  sync.WaitGroup
	)

	for _, src := range g.sources {
		wg.Add(1)
		go func(src SignalSource) {
			defer wg.Done()
			alerts, err := src.Fetch(ctx, req)
			if err != nil {
				g.logf(ctx, slog.LevelWarn, "signals: source failed; continuing without it",
					slog.String("source", src.Name()), slog.Any("error", err))
				return
			}
			if len(alerts) == 0 {
				return
			}
			mu.Lock()
			out = append(out, alerts...)
			mu.Unlock()
		}(src)
	}
	wg.Wait()

	out = dedupeAlerts(out)
	sortAlerts(out)
	return out
}

func (g *Gatherer) logf(ctx context.Context, level slog.Level, msg string, args ...any) {
	if g.logger == nil {
		return
	}
	g.logger.Log(ctx, level, msg, args...)
}

// dedupeAlerts collapses the same real-world event reported by more than one
// source.
//
// This is not cosmetic. GDACS and USGS both report earthquakes, and the score
// charges per alert, so an undeduped overlap would penalise a destination twice
// for one tremor. Matching is on kind, calendar day and rough position
// (~50 km) rather than on any id, because the sources share no identifier —
// GDACS has its own event ids and USGS has its own, and neither references the
// other.
//
// The more severe copy wins, so deduping can never quietly downgrade a warning.
func dedupeAlerts(alerts []Alert) []Alert {
	if len(alerts) < 2 {
		return alerts
	}

	type key struct {
		kind     AlertKind
		day      string
		latCell  int
		lonCell  int
		unplaced string
	}

	// ~0.5 degrees is roughly 50km of latitude — coarse enough to match two
	// sources' differing epicentre estimates, fine enough not to merge two
	// genuinely separate wildfires.
	const cell = 0.5

	seen := make(map[key]int, len(alerts))
	out := make([]Alert, 0, len(alerts))

	for _, a := range alerts {
		k := key{kind: a.Kind}
		if a.Date != nil {
			k.day = a.Date.UTC().Format("2006-01-02")
		}
		if a.Located() {
			k.latCell = int(math.Round(*a.Lat / cell))
			k.lonCell = int(math.Round(*a.Lon / cell))
		} else {
			// Country-scoped alerts have no position to match on, so fall back
			// to the title. Two sources naming the same holiday differently
			// stay separate, which is the safe direction to fail.
			k.unplaced = a.Title
		}

		if idx, ok := seen[k]; ok {
			if effectiveSeverity(a) > effectiveSeverity(out[idx]) {
				out[idx] = a
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, a)
	}
	return out
}

// sortAlerts gives the fan-out a deterministic order. Without it the response
// reorders on every call purely on goroutine scheduling, which makes the score
// look unstable and any cached or diffed output churn.
//
// Most severe first, then soonest, then by title so ties are still stable.
func sortAlerts(a []Alert) {
	sort.SliceStable(a, func(i, j int) bool {
		si, sj := effectiveSeverity(a[i]), effectiveSeverity(a[j])
		if si != sj {
			return si > sj
		}
		di, dj := a[i].Date, a[j].Date
		switch {
		case di != nil && dj != nil && !di.Equal(*dj):
			return di.Before(*dj)
		case di != nil && dj == nil:
			return true
		case di == nil && dj != nil:
			return false
		}
		if a[i].Kind != a[j].Kind {
			return a[i].Kind < a[j].Kind
		}
		return a[i].Title < a[j].Title
	})
}

// effectiveSeverity applies the "zero means unspecified" rule in one place, so
// the scorer and the sort cannot disagree about what an unset severity means.
func effectiveSeverity(a Alert) Severity {
	if a.Severity <= 0 {
		return SeverityMajor
	}
	if a.Severity > SeverityMajor {
		return SeverityMajor
	}
	return a.Severity
}

// withinWindow reports whether a date falls inside the trip window. A source
// with no window (both zero) matches everything, since the caller did not ask
// to filter.
func withinWindow(d, start, end time.Time) bool {
	if start.IsZero() && end.IsZero() {
		return true
	}
	// Compare on calendar days: a holiday is a whole day, and a window that
	// starts at 09:00 on the 1st still contains a holiday dated 00:00 on the 1st.
	day := d.UTC().Truncate(24 * time.Hour)
	if !start.IsZero() && day.Before(start.UTC().Truncate(24*time.Hour)) {
		return false
	}
	if !end.IsZero() && day.After(end.UTC().Truncate(24*time.Hour)) {
		return false
	}
	return true
}
