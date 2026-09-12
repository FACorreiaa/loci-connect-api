package compare

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/google/uuid"
)

// fakeDiscoverer records what it was asked to fill in, and can be held open so a
// test can prove the response does not wait for it.
type fakeDiscoverer struct {
	mu    sync.Mutex
	calls []uuid.UUID
	done  chan struct{}
	block chan struct{}
	err   error
}

func newFakeDiscoverer() *fakeDiscoverer {
	return &fakeDiscoverer{done: make(chan struct{}, 16)}
}

func (f *fakeDiscoverer) DiscoverPOIsForCity(_ context.Context, cityID uuid.UUID, _ string) error {
	f.mu.Lock()
	f.calls = append(f.calls, cityID)
	f.mu.Unlock()

	if f.block != nil {
		<-f.block
	}
	f.done <- struct{}{}
	return f.err
}

func (f *fakeDiscoverer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// waitForCalls blocks until n discoveries have finished, or fails the test.
// A channel rather than a sleep, so this neither flakes nor wastes time.
func (f *fakeDiscoverer) waitForCalls(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-f.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d discoveries ran", i, n)
		}
	}
}

func serviceWithDiscovery(r CityResolver, pois POIFinder, d POIDiscoverer) *Service {
	return newTestService(r, pois).WithDiscovery(d)
}

func threeCities() (*fakeResolver, *cityrepo.Resolved, *cityrepo.Resolved) {
	evora := city("Évora", 38.5714, -7.9135)
	beja := city("Beja", 38.0151, -7.8632)
	return &fakeResolver{cities: map[string]*cityrepo.Resolved{
		"Porto": city("Porto", 41.14961, -8.61099),
		"Évora": evora,
		"Beja":  beja,
	}}, evora, beja
}

// The point of the feature: a city with nothing stored gets filled in.
func TestCompareWeekend_DiscoversPlacesForEmptyCities(t *testing.T) {
	r, evora, beja := threeCities()
	d := newFakeDiscoverer()
	start, end := weekend()

	_, err := serviceWithDiscovery(r, fakePOIs{}, d).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	d.waitForCalls(t, 2)
	d.mu.Lock()
	defer d.mu.Unlock()
	got := map[uuid.UUID]bool{}
	for _, id := range d.calls {
		got[id] = true
	}
	if !got[evora.City.ID] || !got[beja.City.ID] {
		t.Errorf("expected both cities discovered, got %+v", d.calls)
	}
}

// The response must not wait on an LLM round-trip. This holds discovery open for
// the whole call and still expects an answer.
func TestCompareWeekend_DoesNotWaitForDiscovery(t *testing.T) {
	r, _, _ := threeCities()
	d := newFakeDiscoverer()
	d.block = make(chan struct{})
	start, end := weekend()

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		_, err := serviceWithDiscovery(r, fakePOIs{}, d).CompareWeekend(context.Background(), CompareInput{
			OriginCity: "Porto",
			Candidates: []string{"Évora", "Beja"},
			Start:      start,
			End:        end,
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("CompareWeekend blocked on discovery")
	}
	close(d.block)
}

// A city that already has places must not be regenerated; that would be paying
// for something we already have.
func TestCompareWeekend_SkipsDiscoveryWhenCityHasPlaces(t *testing.T) {
	r, evora, beja := threeCities()
	pois := fakePOIs{byCity: map[uuid.UUID]int{evora.City.ID: 6, beja.City.ID: 4}}
	d := newFakeDiscoverer()
	start, end := weekend()

	_, err := serviceWithDiscovery(r, pois, d).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := d.callCount(); got != 0 {
		t.Errorf("discovery ran %d times for cities that already had places", got)
	}
}

// One page load must not fan out into eight generations.
func TestCompareWeekend_CapsDiscoveriesPerRequest(t *testing.T) {
	cities := map[string]*cityrepo.Resolved{"Porto": city("Porto", 41.14961, -8.61099)}
	names := []string{"Évora", "Beja", "Faro", "Tavira", "Lagos", "Sines"}
	for i, n := range names {
		cities[n] = city(n, 38.0+float64(i)*0.1, -8.0-float64(i)*0.1)
	}
	d := newFakeDiscoverer()
	start, end := weekend()

	_, err := serviceWithDiscovery(&fakeResolver{cities: cities}, fakePOIs{}, d).
		CompareWeekend(context.Background(), CompareInput{
			OriginCity:       "Porto",
			Candidates:       names,
			Start:            start,
			End:              end,
			Allow3Candidates: true,
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	d.waitForCalls(t, maxDiscoveriesPerRequest)
	if got := d.callCount(); got > maxDiscoveriesPerRequest {
		t.Errorf("discovery ran %d times for %d empty cities, cap is %d",
			got, len(names), maxDiscoveriesPerRequest)
	}
}

// Re-running the same comparison must not pay twice.
func TestCompareWeekend_CooldownStopsRepeatDiscovery(t *testing.T) {
	r, _, _ := threeCities()
	d := newFakeDiscoverer()
	svc := serviceWithDiscovery(r, fakePOIs{}, d)
	start, end := weekend()

	in := CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	}
	if _, err := svc.CompareWeekend(context.Background(), in); err != nil {
		t.Fatalf("first compare: %v", err)
	}
	d.waitForCalls(t, 2)

	if _, err := svc.CompareWeekend(context.Background(), in); err != nil {
		t.Fatalf("second compare: %v", err)
	}
	if got := d.callCount(); got != 2 {
		t.Errorf("discovery ran %d times across two identical comparisons, want 2", got)
	}
}

// A failed generation must not fail, or even reach, the comparison.
func TestCompareWeekend_DiscoveryFailureIsInvisible(t *testing.T) {
	r, _, _ := threeCities()
	d := newFakeDiscoverer()
	d.err = errors.New("model unavailable")
	start, end := weekend()

	out, err := serviceWithDiscovery(r, fakePOIs{}, d).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	})
	if err != nil {
		t.Fatalf("a discovery failure must not fail the comparison: %v", err)
	}
	if len(out.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(out.Columns))
	}
	d.waitForCalls(t, 2)
}

// Discovery is optional; without it the old behaviour stands.
func TestCompareWeekend_WorksWithoutDiscovery(t *testing.T) {
	r, _, _ := threeCities()
	start, end := weekend()

	out, err := newTestService(r, fakePOIs{}).CompareWeekend(context.Background(), CompareInput{
		OriginCity: "Porto",
		Candidates: []string{"Évora", "Beja"},
		Start:      start,
		End:        end,
	})
	if err != nil || len(out.Columns) != 2 {
		t.Fatalf("got %d columns, err %v", len(out.Columns), err)
	}
}

// A city the resolver could not persist has no id, so there is nothing to attach
// generated places to and nothing to look them up by later.
func TestMaybeDiscover_IgnoresCityWithoutAnID(t *testing.T) {
	d := newFakeDiscoverer()
	svc := serviceWithDiscovery(&fakeResolver{}, fakePOIs{}, d)

	if svc.maybeDiscover(uuid.Nil, "Porto") {
		t.Error("expected no discovery for a city with no id")
	}
	if got := d.callCount(); got != 0 {
		t.Errorf("discovery ran %d times", got)
	}
}
