package localcontext

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	name   string
	alerts []Alert
	err    error
	delay  time.Duration
	calls  int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	f.calls++
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return f.alerts, f.err
}

type fakeCountry struct {
	code  string
	err   error
	calls int
}

func (f *fakeCountry) CountryCode(ctx context.Context, lat, lon float64) (string, error) {
	f.calls++
	return f.code, f.err
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

// The core promise: a broken source costs its own contribution and nothing
// else. Trip context is layered on top of the itinerary and must never take it
// down with it.
func TestGatherer_OneFailingSourceDoesNotLoseTheOthers(t *testing.T) {
	good := &fakeSource{name: "good", alerts: []Alert{{Kind: AlertHoliday, Title: "Carnival"}}}
	bad := &fakeSource{name: "bad", err: errors.New("provider on fire")}

	g := NewGatherer(&fakeCountry{code: "PT"}, nil, bad, good)
	got := g.Gather(context.Background(), 38.7, -9.1, time.Time{}, time.Time{})

	if len(got) != 1 {
		t.Fatalf("expected the healthy source's alert, got %d", len(got))
	}
	if got[0].Title != "Carnival" {
		t.Errorf("got %q", got[0].Title)
	}
}

// Every country-scoped source would otherwise repeat the same lookup.
func TestGatherer_ResolvesCountryOnceAndSharesIt(t *testing.T) {
	c := &fakeCountry{code: "PT"}
	a := &fakeSource{name: "a"}
	b := &fakeSource{name: "b"}

	NewGatherer(c, nil, a, b).Gather(context.Background(), 38.7, -9.1, time.Time{}, time.Time{})

	if c.calls != 1 {
		t.Errorf("expected 1 country lookup, got %d", c.calls)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("expected both sources called once, got %d and %d", a.calls, b.calls)
	}
}

// A geocoder outage must not take the whole gather down — sources that do not
// need a country still have something to say.
func TestGatherer_CountryFailureStillRunsSources(t *testing.T) {
	c := &fakeCountry{err: errors.New("geocoder down")}
	src := &fakeSource{name: "s", alerts: []Alert{{Kind: AlertHazard, Title: "Quake"}}}

	got := NewGatherer(c, nil, src).Gather(context.Background(), 38.7, -9.1, time.Time{}, time.Time{})

	if src.calls != 1 {
		t.Errorf("source should still have run, calls=%d", src.calls)
	}
	if len(got) != 1 {
		t.Errorf("expected the alert through, got %d", len(got))
	}
}

func TestGatherer_PassesResolvedCountryToSources(t *testing.T) {
	var seen string
	src := &fakeSource{name: "s"}
	g := NewGatherer(&fakeCountry{code: "PT"}, nil, sourceFunc{name: "probe", fn: func(_ context.Context, req SignalRequest) ([]Alert, error) {
		seen = req.CountryCode
		return nil, nil
	}}, src)

	g.Gather(context.Background(), 38.7, -9.1, time.Time{}, time.Time{})
	if seen != "PT" {
		t.Errorf("country code: got %q, want PT", seen)
	}
}

// Without a stable order the response reorders on every call purely on
// goroutine scheduling, which makes the score look unstable.
func TestGatherer_OrdersMostSevereFirstThenSoonest(t *testing.T) {
	src := &fakeSource{name: "s", alerts: []Alert{
		{Kind: AlertHoliday, Title: "minor later", Severity: SeverityMinor, Date: day(2026, 9, 5)},
		{Kind: AlertHazard, Title: "major", Severity: SeverityMajor, Date: day(2026, 9, 4)},
		{Kind: AlertHoliday, Title: "minor sooner", Severity: SeverityMinor, Date: day(2026, 9, 1)},
	}}

	got := NewGatherer(nil, nil, src).Gather(context.Background(), 0, 0, time.Time{}, time.Time{})

	want := []string{"major", "minor sooner", "minor later"}
	if len(got) != len(want) {
		t.Fatalf("got %d alerts", len(got))
	}
	for i := range want {
		if got[i].Title != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i].Title, want[i])
		}
	}
}

// Nil is a supported, meaningful state: signals switched off.
func TestGatherer_NilIsSafeAndDisabled(t *testing.T) {
	var g *Gatherer
	if g.Enabled() {
		t.Error("a nil Gatherer must report disabled")
	}
	if got := g.Gather(context.Background(), 38.7, -9.1, time.Time{}, time.Time{}); got != nil {
		t.Errorf("expected no alerts, got %d", len(got))
	}
}

func TestGatherer_NoSourcesIsDisabled(t *testing.T) {
	if NewGatherer(nil, nil).Enabled() {
		t.Error("a Gatherer with no sources must report disabled")
	}
}

func TestEffectiveSeverity(t *testing.T) {
	tests := []struct {
		name string
		in   Severity
		want Severity
	}{
		// The back-compat rule the existing score tests depend on.
		{"unset means full weight", 0, SeverityMajor},
		{"negative means full weight", -1, SeverityMajor},
		{"minor passes through", SeverityMinor, SeverityMinor},
		{"moderate passes through", SeverityModerate, SeverityModerate},
		{"above one is clamped", 5, SeverityMajor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveSeverity(Alert{Severity: tt.in}); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithinWindow(t *testing.T) {
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		d    time.Time
		want bool
	}{
		// A holiday is a whole day; a window starting at 09:00 still contains
		// a holiday timestamped midnight the same day.
		{"midnight on the start day", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), true},
		{"inside", time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC), true},
		{"late on the end day", time.Date(2026, 9, 3, 23, 0, 0, 0, time.UTC), true},
		{"day before", time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC), false},
		{"day after", time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withinWindow(tt.d, start, end); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	// No window means the caller did not ask to filter.
	if !withinWindow(time.Now(), time.Time{}, time.Time{}) {
		t.Error("an unset window must match everything")
	}
}

// sourceFunc adapts a function to SignalSource for tests.
type sourceFunc struct {
	name string
	fn   func(context.Context, SignalRequest) ([]Alert, error)
}

func (s sourceFunc) Name() string { return s.name }
func (s sourceFunc) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	return s.fn(ctx, req)
}

// --- cross-source dedupe ---------------------------------------------------
//
// GDACS and USGS both report earthquakes and the score charges per alert, so an
// undeduped overlap penalises a destination twice for one tremor.

func located(lat, lon float64) (*float64, *float64) { return &lat, &lon }

func TestDedupeAlerts_CollapsesTheSameQuakeFromTwoSources(t *testing.T) {
	when := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	laterSameDay := time.Date(2026, 9, 2, 3, 4, 0, 0, time.UTC)
	lat1, lon1 := located(38.60, -9.30)
	lat2, lon2 := located(38.63, -9.27) // a few km apart, as two agencies would differ

	got := dedupeAlerts([]Alert{
		{Kind: AlertHazard, Title: "M5.4 quake", Date: &when, Lat: lat1, Lon: lon1, Severity: SeverityModerate, Source: SourceUSGS},
		{Kind: AlertHazard, Title: "Earthquake in Portugal", Date: &laterSameDay, Lat: lat2, Lon: lon2, Severity: SeverityMinor, Source: SourceGDACS},
	})

	if len(got) != 1 {
		t.Fatalf("expected one alert, got %d: %+v", len(got), got)
	}
	// The more severe copy must win — dedupe may never quietly downgrade a warning.
	if got[0].Severity != SeverityModerate {
		t.Errorf("severity: got %v, want the more severe %v", got[0].Severity, SeverityModerate)
	}
}

func TestDedupeAlerts_KeepsGenuinelySeparateEvents(t *testing.T) {
	when := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	lisbonLat, lisbonLon := located(38.72, -9.14)
	portoLat, portoLon := located(41.15, -8.61)

	got := dedupeAlerts([]Alert{
		{Kind: AlertHazard, Title: "Wildfire near Lisbon", Date: &when, Lat: lisbonLat, Lon: lisbonLon},
		{Kind: AlertHazard, Title: "Wildfire near Porto", Date: &when, Lat: portoLat, Lon: portoLon},
	})
	if len(got) != 2 {
		t.Errorf("two separate wildfires must both survive, got %d", len(got))
	}
}

func TestDedupeAlerts_DifferentDaysAreDifferentEvents(t *testing.T) {
	d1 := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	lat, lon := located(38.60, -9.30)
	lat2, lon2 := located(38.60, -9.30)

	got := dedupeAlerts([]Alert{
		{Kind: AlertHazard, Title: "quake", Date: &d1, Lat: lat, Lon: lon},
		{Kind: AlertHazard, Title: "aftershock", Date: &d2, Lat: lat2, Lon: lon2},
	})
	if len(got) != 2 {
		t.Errorf("same place on different days is two events, got %d", len(got))
	}
}

func TestDedupeAlerts_DifferentKindsNeverMerge(t *testing.T) {
	when := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	lat, lon := located(38.60, -9.30)
	lat2, lon2 := located(38.60, -9.30)

	got := dedupeAlerts([]Alert{
		{Kind: AlertHazard, Title: "wildfire", Date: &when, Lat: lat, Lon: lon},
		{Kind: AlertHoliday, Title: "holiday", Date: &when, Lat: lat2, Lon: lon2},
	})
	if len(got) != 2 {
		t.Errorf("a wildfire and a holiday are not the same event, got %d", len(got))
	}
}

// Country-scoped alerts have no position, so they fall back to matching on
// title. Failing to merge is the safe direction.
func TestDedupeAlerts_UnlocatedMatchOnTitle(t *testing.T) {
	when := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	got := dedupeAlerts([]Alert{
		{Kind: AlertHoliday, Title: "Republic Day", Date: &when, Severity: SeverityMinor},
		{Kind: AlertHoliday, Title: "Republic Day", Date: &when, Severity: SeverityModerate},
		{Kind: AlertHoliday, Title: "Some Other Day", Date: &when},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %+v", len(got), got)
	}
	for _, a := range got {
		if a.Title == "Republic Day" && a.Severity != SeverityModerate {
			t.Errorf("the more severe copy should win, got %v", a.Severity)
		}
	}
}

// The whole reason dedupe exists: two reports of one quake must cost one
// penalty, not two.
func TestScore_DuplicateHazardIsChargedOnce(t *testing.T) {
	when := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	lat1, lon1 := located(38.60, -9.30)
	lat2, lon2 := located(38.62, -9.28)

	deduped := dedupeAlerts([]Alert{
		{Kind: AlertHazard, Title: "M5.4", Date: &when, Lat: lat1, Lon: lon1, Severity: SeverityModerate},
		{Kind: AlertHazard, Title: "Earthquake", Date: &when, Lat: lat2, Lon: lon2, Severity: SeverityModerate},
	})

	if got := disruptionPenalty(deduped); got != 5 {
		t.Errorf("one event should cost one moderate penalty of 5, got %d", got)
	}
}

func TestGatherer_DedupesAcrossSources(t *testing.T) {
	when := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	lat1, lon1 := located(38.60, -9.30)
	lat2, lon2 := located(38.62, -9.28)

	usgs := &fakeSource{name: SourceUSGS, alerts: []Alert{
		{Kind: AlertHazard, Title: "M5.4", Date: &when, Lat: lat1, Lon: lon1, Severity: SeverityModerate, Source: SourceUSGS},
	}}
	gdacs := &fakeSource{name: SourceGDACS, alerts: []Alert{
		{Kind: AlertHazard, Title: "Earthquake in Portugal", Date: &when, Lat: lat2, Lon: lon2, Severity: SeverityMinor, Source: SourceGDACS},
	}}

	got := NewGatherer(nil, nil, usgs, gdacs).Gather(context.Background(), 38.72, -9.14, time.Time{}, time.Time{})
	if len(got) != 1 {
		t.Fatalf("expected the duplicate collapsed, got %d: %+v", len(got), got)
	}
}
