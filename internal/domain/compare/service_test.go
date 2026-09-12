package compare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

// --- fakes ---

// fakeResolver answers from a fixed table of cities, and fails for anything
// else with whatever error the test wants — which is how the geocoder-outage
// and unknown-city paths are distinguished.
type fakeResolver struct {
	cities map[string]*cityrepo.Resolved
	err    error
	calls  []cityrepo.ResolveQuery
}

func (f *fakeResolver) Resolve(_ context.Context, q cityrepo.ResolveQuery) (*cityrepo.Resolved, error) {
	f.calls = append(f.calls, q)
	// Coordinates are honoured before any failure, because the real resolver
	// short-circuits on them before it can reach a geocoder at all. A fake that
	// failed first would let a test "prove" an outage on a path that never
	// touches the provider.
	if q.Lat != 0 || q.Lon != 0 {
		return &cityrepo.Resolved{
			City: locitypes.CityDetail{Name: q.Name}, Lat: q.Lat, Lon: q.Lon,
			Source: cityrepo.ResolvedFromClient,
		}, nil
	}
	if f.err != nil {
		return nil, f.err
	}
	if r, ok := f.cities[q.Name]; ok {
		return r, nil
	}
	return nil, &cityrepo.AmbiguousCityError{Query: q.Name}
}

type fakePOIs struct {
	byCity map[uuid.UUID]int
}

func (f fakePOIs) GetPOIsByCityID(_ context.Context, id uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	n := f.byCity[id]
	out := make([]locitypes.POIDetailedInfo, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, locitypes.POIDetailedInfo{
			ID: uuid.New(), Name: fmt.Sprintf("Place %d", i), Category: "culture",
			Latitude: 38.7, Longitude: -9.1,
		})
	}
	return out, nil
}

type fakeWeather struct{}

func (fakeWeather) Forecast(context.Context, float64, float64, int) ([]localcontext.WeatherDay, error) {
	return []localcontext.WeatherDay{
		{Date: time.Now(), HighC: 24, LowC: 15, Condition: "Clear", PrecipProb: 0.05},
		{Date: time.Now().Add(24 * time.Hour), HighC: 25, LowC: 16, Condition: "Clear", PrecipProb: 0.05},
	}, nil
}

type fakePlans struct{ plan string }

func (f fakePlans) EffectivePlan(context.Context, uuid.UUID) (string, error) { return f.plan, nil }

func city(name string, lat, lon float64) *cityrepo.Resolved {
	id := uuid.New()
	return &cityrepo.Resolved{
		City:   locitypes.CityDetail{ID: id, Name: name, Country: "Portugal"},
		Lat:    lat,
		Lon:    lon,
		Source: cityrepo.ResolvedFromDB,
	}
}

func newTestService(resolver CityResolver, pois POIFinder) *Service {
	return NewService(
		resolver,
		pois,
		fakeWeather{},
		false,
		localcontext.StubTransportWithDrive{Fallback: localcontext.StubTransport{}},
		localcontext.BookingComDeepLink{},
		localcontext.OpenTableDeepLink{},
		fakePlans{plan: "free"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func weekend() (time.Time, time.Time) {
	start := time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)
	return start, start.Add(48 * time.Hour)
}

// --- tests ---

// The reported bug, from the other side: Porto is not in the table, the resolver
// finds it anyway, and the comparison succeeds.
func TestCompareWeekend_ResolvesOriginAndCandidates(t *testing.T) {
	r := &fakeResolver{cities: map[string]*cityrepo.Resolved{
		"Porto": city("Porto", 41.14961, -8.61099),
		"Évora": city("Évora", 38.5714, -7.9135),
		"Beja":  city("Beja", 38.0151, -7.8632),
	}}
	start, end := weekend()

	out, err := newTestService(r, fakePOIs{}).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.OriginCity != "Porto" {
		t.Errorf("origin: got %q", out.OriginCity)
	}
	if len(out.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(out.Columns))
	}
	if out.Columns[0].DistanceKm <= 0 {
		t.Errorf("distance was not computed: %v", out.Columns[0].DistanceKm)
	}
}

// Supplied coordinates must skip name resolution entirely.
func TestResolveOrigin_UsesSuppliedCoordinates(t *testing.T) {
	r := &fakeResolver{}
	s := newTestService(r, fakePOIs{})

	lat, lon, name, err := s.resolveOrigin(context.Background(), CompareInput{
		OriginCity: "Somewhere", OriginLat: 41.1, OriginLon: -8.6,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lat != 41.1 || lon != -8.6 || name != "Somewhere" {
		t.Errorf("got %v,%v,%q", lat, lon, name)
	}
}

func TestResolveOrigin_RequiresSomething(t *testing.T) {
	s := newTestService(&fakeResolver{}, fakePOIs{})
	if _, _, _, err := s.resolveOrigin(context.Background(), CompareInput{}); err == nil {
		t.Fatal("expected an error with neither a name nor coordinates")
	}
}

// The wrap must not swallow the sentinel: the handler decides the status code by
// unwrapping this, so a flattened error is how an outage becomes a 400.
func TestResolveOrigin_PreservesGeocoderSentinel(t *testing.T) {
	r := &fakeResolver{err: fmt.Errorf("%w: 503", cityrepo.ErrGeocoderUnavailable)}
	s := newTestService(r, fakePOIs{})

	_, _, _, err := s.resolveOrigin(context.Background(), CompareInput{OriginCity: "Porto"})
	if !errors.Is(err, cityrepo.ErrGeocoderUnavailable) {
		t.Fatalf("sentinel lost through the wrap: %v", err)
	}
}

// One bad candidate out of three still leaves a usable comparison.
func TestCompareWeekend_SkipsUnresolvableCandidate(t *testing.T) {
	r := &fakeResolver{cities: map[string]*cityrepo.Resolved{
		"Porto": city("Porto", 41.14961, -8.61099),
		"Évora": city("Évora", 38.5714, -7.9135),
		"Beja":  city("Beja", 38.0151, -7.8632),
	}}
	start, end := weekend()

	out, err := newTestService(r, fakePOIs{}).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Zzzqqx", "Beja"},
		Start:      start,
		End:        end,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Columns) != 2 {
		t.Fatalf("expected the 2 resolvable columns, got %d", len(out.Columns))
	}
}

// When every column fails because the geocoder is down, the cause has to travel
// with ErrTooFewResolvable. Otherwise the caller reports "give me two cities" —
// a complaint about input — for an outage that is entirely ours. That
// misdiagnosis is what the original bug report looked like.
func TestCompareWeekend_OutageSurvivesTooFewResolvable(t *testing.T) {
	r := &fakeResolver{
		cities: map[string]*cityrepo.Resolved{"Porto": city("Porto", 41.14961, -8.61099)},
	}
	start, end := weekend()
	s := newTestService(r, fakePOIs{})

	// Confirm the origin resolves from the table first, then kill the provider
	// so only the candidate lookups fail.
	if _, _, _, err := s.resolveOrigin(context.Background(), CompareInput{OriginCity: "Porto"}); err != nil {
		t.Fatalf("origin should resolve: %v", err)
	}
	r.err = fmt.Errorf("%w: 503", cityrepo.ErrGeocoderUnavailable)

	_, err := s.CompareWeekend(context.Background(), CompareInput{
		OriginLat:  41.14961,
		OriginLon:  -8.61099,
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	})
	if !errors.Is(err, ErrTooFewResolvable) {
		t.Errorf("expected ErrTooFewResolvable, got %v", err)
	}
	if !errors.Is(err, cityrepo.ErrGeocoderUnavailable) {
		t.Fatalf("the underlying outage was lost: %v", err)
	}
}

// With no outage, too few candidates really is about the input.
func TestCompareWeekend_UnresolvableCandidatesAreInvalidInput(t *testing.T) {
	r := &fakeResolver{cities: map[string]*cityrepo.Resolved{
		"Porto": city("Porto", 41.14961, -8.61099),
	}}
	start, end := weekend()

	_, err := newTestService(r, fakePOIs{}).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Zzzqqx", "Qqqzzx"},
		Start:      start,
		End:        end,
	})
	if !errors.Is(err, ErrTooFewResolvable) {
		t.Fatalf("got %v, want ErrTooFewResolvable", err)
	}
	if errors.Is(err, cityrepo.ErrGeocoderUnavailable) {
		t.Errorf("no outage happened, but the error claims one: %v", err)
	}
	if !errors.Is(err, cityrepo.ErrCityUnresolvable) {
		t.Errorf("expected the unresolvable-city cause to travel: %v", err)
	}
}

// A city we have just created has no POIs yet. That is a thin column, not a
// failure: weather, distance and drive time still answer the question.
func TestCompareWeekend_CityWithNoPOIsStillProducesAColumn(t *testing.T) {
	evora := city("Évora", 38.5714, -7.9135)
	beja := city("Beja", 38.0151, -7.8632)
	r := &fakeResolver{cities: map[string]*cityrepo.Resolved{
		"Porto": city("Porto", 41.14961, -8.61099),
		"Évora": evora,
		"Beja":  beja,
	}}
	// Only Évora has anything stored.
	pois := fakePOIs{byCity: map[uuid.UUID]int{evora.City.ID: 6}}
	start, end := weekend()

	out, err := newTestService(r, pois).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(out.Columns))
	}

	bejaCol := out.Columns[1]
	if bejaCol.CityName != "Beja" {
		t.Fatalf("unexpected column order: %q", bejaCol.CityName)
	}
	if len(bejaCol.TopPois) != 0 {
		t.Errorf("expected no POIs for Beja, got %d", len(bejaCol.TopPois))
	}
	if bejaCol.TravelMins <= 0 || bejaCol.DistanceKm <= 0 {
		t.Errorf("the column lost its logistics: %v / %v", bejaCol.TravelMins, bejaCol.DistanceKm)
	}
	if len(bejaCol.Cons) == 0 {
		t.Error("a POI-less column should say so in its cons")
	}
}
