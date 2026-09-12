package city

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
	"github.com/google/uuid"
)

func ptr(f float64) *float64 { return &f }

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- fakes ---

type fakeRepo struct {
	candidates []locitypes.CityDetail
	candErr    error

	near    *locitypes.CityDetail
	nearErr error

	saveID  uuid.UUID
	saveErr error

	enrichErr error

	searchResult []locitypes.CityDetail

	mu       sync.Mutex
	saved    []locitypes.CityDetail
	enriched []uuid.UUID
}

func (f *fakeRepo) FindCityCandidates(context.Context, string, int) ([]locitypes.CityDetail, error) {
	return f.candidates, f.candErr
}

func (f *fakeRepo) FindCityNear(context.Context, float64, float64, float64) (*locitypes.CityDetail, error) {
	return f.near, f.nearErr
}

func (f *fakeRepo) SaveCity(_ context.Context, c locitypes.CityDetail) (uuid.UUID, error) {
	f.mu.Lock()
	f.saved = append(f.saved, c)
	f.mu.Unlock()
	if f.saveErr != nil {
		return uuid.Nil, f.saveErr
	}
	return f.saveID, nil
}

func (f *fakeRepo) EnrichCity(_ context.Context, id uuid.UUID, _, _ float64, _, _ string) error {
	f.mu.Lock()
	f.enriched = append(f.enriched, id)
	f.mu.Unlock()
	return f.enrichErr
}

func (f *fakeRepo) SearchCitiesByName(context.Context, string, int) ([]locitypes.CityDetail, error) {
	return f.searchResult, nil
}

func (f *fakeRepo) savedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

func (f *fakeRepo) enrichedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.enriched)
}

// The rest of Repository, unused here.
func (f *fakeRepo) FindCityByNameAndCountry(context.Context, string, string) (*locitypes.CityDetail, error) {
	return nil, nil
}

func (f *fakeRepo) FindCityByFuzzyName(context.Context, string) (*locitypes.CityDetail, error) {
	return nil, nil
}

func (f *fakeRepo) GetCityIDByName(context.Context, string) (uuid.UUID, error)   { return uuid.Nil, nil }
func (f *fakeRepo) GetAllCities(context.Context) ([]locitypes.CityDetail, error) { return nil, nil }
func (f *fakeRepo) GetCityByID(context.Context, uuid.UUID) (*locitypes.CityDetail, error) {
	return nil, nil
}

func (f *fakeRepo) FindSimilarCities(context.Context, []float32, int) ([]locitypes.CityDetail, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateCityEmbedding(context.Context, uuid.UUID, []float32) error { return nil }
func (f *fakeRepo) GetCitiesWithoutEmbeddings(context.Context, int) ([]locitypes.CityDetail, error) {
	return nil, nil
}

func (f *fakeRepo) GetCity(context.Context, float64, float64) (uuid.UUID, string, error) {
	return uuid.Nil, "", nil
}

type fakeForward struct {
	places []geocode.Place
	err    error
	calls  int64
	// entered is signalled on every call and block holds the call open, so a
	// concurrency test can guarantee the callers genuinely overlap rather than
	// finishing one after another faster than they are spawned.
	entered chan struct{}
	block   chan struct{}
}

func (f *fakeForward) Search(context.Context, string, int) ([]geocode.Place, error) {
	atomic.AddInt64(&f.calls, 1)
	if f.entered != nil {
		// Non-blocking: the test only needs to learn that a call started, and a
		// blocking send would deadlock every caller after the first once the
		// test has stopped reading.
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.places, nil
}

func (f *fakeForward) callCount() int64 { return atomic.LoadInt64(&f.calls) }

var portoPT = geocode.Place{
	Name: "Porto", Country: "Portugal", CountryCode: "PT", Admin1: "Porto",
	Lat: 41.14961, Lon: -8.61099, Population: 249633, FeatureCode: "PPLA",
}

var portoBR = geocode.Place{
	Name: "Porto", Country: "Brazil", CountryCode: "BR", Admin1: "Sergipe",
	Lat: -11.45, Lon: -37.29, Population: 1500, FeatureCode: "PPL",
}

// --- tests ---

// Coordinates are unambiguous, so a name lookup would only be a chance to get it
// wrong.
func TestResolve_ClientCoordinatesShortCircuit(t *testing.T) {
	repo := &fakeRepo{}
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	r := NewResolver(repo, fwd, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Anywhere", Lat: 41.1, Lon: -8.6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != ResolvedFromClient {
		t.Errorf("source: got %q, want client", got.Source)
	}
	if got.Lat != 41.1 || got.Lon != -8.6 {
		t.Errorf("coordinates: got %v,%v", got.Lat, got.Lon)
	}
	if fwd.callCount() != 0 {
		t.Errorf("geocoder was called %d times for a coordinate query", fwd.callCount())
	}
}

// Supplying coordinates used to cost you the city_id, and therefore every POI in
// the column.
func TestResolve_ClientCoordinatesAttachNearbyCity(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{near: &locitypes.CityDetail{
		ID: id, Name: "Porto", Country: "Portugal",
		CenterLatitude: ptr(41.14961), CenterLongitude: ptr(-8.61099),
	}}
	r := NewResolver(repo, &fakeForward{}, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Lat: 41.15, Lon: -8.61})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.City.ID != id {
		t.Errorf("city id: got %v, want %v", got.City.ID, id)
	}
	// The caller's coordinates still win over the stored centre.
	if got.Lat != 41.15 {
		t.Errorf("lat: got %v, want the supplied 41.15", got.Lat)
	}
}

// The hot path. If this ever starts geocoding, every comparison becomes a
// network call to a rate-limited free API.
func TestResolve_DatabaseHitSkipsGeocoder(t *testing.T) {
	repo := &fakeRepo{candidates: []locitypes.CityDetail{{
		ID: uuid.New(), Name: "Porto", Country: "Portugal",
		CenterLatitude: ptr(41.14961), CenterLongitude: ptr(-8.61099),
	}}}
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	r := NewResolver(repo, fwd, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != ResolvedFromDB {
		t.Errorf("source: got %q, want db", got.Source)
	}
	if fwd.callCount() != 0 {
		t.Errorf("geocoder was called %d times for a stored city", fwd.callCount())
	}
	if repo.savedCount() != 0 {
		t.Errorf("a stored city must not be written again")
	}
}

// A stub row with no coordinates is healed in place. Inserting a corrected
// sibling would strand every POI already pointing at the stub.
func TestResolve_CoordlessRowIsBackfilledNotDuplicated(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{candidates: []locitypes.CityDetail{{
		ID: id, Name: "Porto", Country: "Unknown", StateProvince: "Unknown",
	}}}
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	r := NewResolver(repo, fwd, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.enrichedCount() != 1 {
		t.Errorf("expected one enrich, got %d", repo.enrichedCount())
	}
	if repo.savedCount() != 0 {
		t.Errorf("backfilling must not insert a second row (got %d saves)", repo.savedCount())
	}
	if got.City.ID != id {
		t.Errorf("the existing row's id must survive: got %v, want %v", got.City.ID, id)
	}
	if got.City.Country != "Portugal" {
		t.Errorf("country should be promoted from Unknown, got %q", got.City.Country)
	}
	if got.Lat != portoPT.Lat {
		t.Errorf("lat: got %v, want %v", got.Lat, portoPT.Lat)
	}
}

// The bug this whole change exists for: a city nobody has chatted about.
func TestResolve_UnknownCityGeocodesAndPersists(t *testing.T) {
	newID := uuid.New()
	repo := &fakeRepo{saveID: newID}
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	r := NewResolver(repo, fwd, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != ResolvedFromGeocoder || !got.Created {
		t.Errorf("source/created: got %q/%v", got.Source, got.Created)
	}
	if got.City.ID != newID {
		t.Errorf("id: got %v, want %v", got.City.ID, newID)
	}
	if repo.savedCount() != 1 {
		t.Fatalf("expected one save, got %d", repo.savedCount())
	}
	// The real country, not the "Unknown" every other create-on-demand path writes.
	if saved := repo.saved[0]; saved.Country != "Portugal" || saved.StateProvince != "Porto" {
		t.Errorf("saved country/state: got %q/%q", saved.Country, saved.StateProvince)
	}
}

// A typo must not mint a row called "Prto".
func TestResolve_PersistsCanonicalNameNotUserTyping(t *testing.T) {
	repo := &fakeRepo{saveID: uuid.New()}
	r := NewResolver(repo, &fakeForward{places: []geocode.Place{portoPT}}, quietLogger())

	if _, err := r.Resolve(context.Background(), ResolveQuery{Name: "Prto"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := repo.saved[0].Name; got != "Porto" {
		t.Errorf("saved name: got %q, want the canonical Porto", got)
	}
}

// Porto means Portugal's second city, not a Brazilian village of 1,500.
func TestResolve_PrefersHigherPopulation(t *testing.T) {
	repo := &fakeRepo{saveID: uuid.New()}
	// Deliberately provider-ordered with Brazil first.
	r := NewResolver(repo, &fakeForward{places: []geocode.Place{portoBR, portoPT}}, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.City.Country != "Portugal" {
		t.Errorf("country: got %q, want Portugal", got.City.Country)
	}
}

func TestResolve_CountryCodeFilterWins(t *testing.T) {
	repo := &fakeRepo{saveID: uuid.New()}
	r := NewResolver(repo, &fakeForward{places: []geocode.Place{portoPT, portoBR}}, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto", CountryCode: "BR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.City.Country != "Brazil" {
		t.Errorf("country: got %q, want Brazil", got.City.Country)
	}
}

// Rivers, regions and airports share city names, and some outrank the city.
func TestResolve_DropsNonPopulatedPlaces(t *testing.T) {
	region := geocode.Place{
		Name: "Porto", Country: "Portugal", CountryCode: "PT",
		Lat: 41.2, Lon: -8.4, Population: 1800000, FeatureCode: "ADM1",
	}
	repo := &fakeRepo{saveID: uuid.New()}
	// The region has by far the larger population, so only the feature-code
	// filter can keep it from winning.
	r := NewResolver(repo, &fakeForward{places: []geocode.Place{region, portoPT}}, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Lat != portoPT.Lat {
		t.Errorf("resolved the region, not the city: got lat %v", got.Lat)
	}
}

// LOWER('Evora') never equals 'Évora'.
func TestResolve_FoldsAccentsAgainstStoredRows(t *testing.T) {
	repo := &fakeRepo{candidates: []locitypes.CityDetail{
		{ID: uuid.New(), Name: "Evoramonte", Country: "Portugal", CenterLatitude: ptr(38.7), CenterLongitude: ptr(-7.7)},
		{ID: uuid.New(), Name: "Évora", Country: "Portugal", CenterLatitude: ptr(38.5714), CenterLongitude: ptr(-7.9135)},
	}}
	fwd := &fakeForward{}
	r := NewResolver(repo, fwd, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Evora"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.City.Name != "Évora" {
		t.Errorf("got %q, want the accented exact match Évora", got.City.Name)
	}
	if fwd.callCount() != 0 {
		t.Errorf("an accented stored row should not need the geocoder")
	}
}

// The single most important mapping in the change: an outage is ours, not the
// user's, and must never be reported as bad input.
func TestResolve_GeocoderOutageIsTypedAndDoesNotUseCoordlessRow(t *testing.T) {
	repo := &fakeRepo{candidates: []locitypes.CityDetail{
		{ID: uuid.New(), Name: "Porto", Country: "Unknown"},
	}}
	fwd := &fakeForward{err: fmt.Errorf("%w: status 503", geocode.ErrUnavailable)}
	r := NewResolver(repo, fwd, quietLogger())

	_, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if !errors.Is(err, ErrGeocoderUnavailable) {
		t.Fatalf("got %v, want ErrGeocoderUnavailable", err)
	}
	if errors.Is(err, ErrCityUnresolvable) {
		t.Errorf("an outage must not also read as an unresolvable name: %v", err)
	}
}

func TestResolve_NoHitsIsUnresolvableWithSuggestions(t *testing.T) {
	// Nothing populated, so ranking rejects them, but they are still worth
	// offering back as "did you mean".
	near := geocode.Place{Name: "Porto Covo", Country: "Portugal", CountryCode: "PT", FeatureCode: "ADM2", Lat: 37.8, Lon: -8.7}
	r := NewResolver(&fakeRepo{}, &fakeForward{places: []geocode.Place{near}}, quietLogger())

	_, err := r.Resolve(context.Background(), ResolveQuery{Name: "Zzzqqx"})
	if !errors.Is(err, ErrCityUnresolvable) {
		t.Fatalf("got %v, want ErrCityUnresolvable", err)
	}
	var amb *AmbiguousCityError
	if !errors.As(err, &amb) {
		t.Fatalf("got %v, want an AmbiguousCityError", err)
	}
	if len(amb.Suggestions) != 1 || amb.Suggestions[0].Name != "Porto Covo" {
		t.Errorf("suggestions: got %+v", amb.Suggestions)
	}
}

// We know where the city is. A failed insert is no reason to fail the answer.
func TestResolve_PersistFailureStillReturnsCoordinates(t *testing.T) {
	repo := &fakeRepo{saveErr: errors.New("disk on fire")}
	r := NewResolver(repo, &fakeForward{places: []geocode.Place{portoPT}}, quietLogger())

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err != nil {
		t.Fatalf("a persist failure must not fail the resolve: %v", err)
	}
	if got.City.ID != uuid.Nil {
		t.Errorf("id: got %v, want uuid.Nil", got.City.ID)
	}
	if got.Lat != portoPT.Lat || got.Lon != portoPT.Lon {
		t.Errorf("coordinates lost: %v,%v", got.Lat, got.Lon)
	}
}

// A database failure is not "no such city"; saying so would send the user off to
// fix a name that was never the problem.
func TestResolve_DatabaseErrorIsNotUnresolvable(t *testing.T) {
	repo := &fakeRepo{candErr: errors.New("connection refused")}
	r := NewResolver(repo, &fakeForward{places: []geocode.Place{portoPT}}, quietLogger())

	_, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrCityUnresolvable) || errors.Is(err, ErrGeocoderUnavailable) {
		t.Errorf("a DB failure must stay untyped so it maps to Internal: %v", err)
	}
}

// Eight candidate columns resolve concurrently on every comparison, so the
// resolver has to be safe under that.
//
// This asserts safety and the internal invariant that we never write more rows
// than we geocoded. It deliberately does NOT assert "exactly one geocoder call":
// whether a caller joins the in-flight request or starts the next one depends on
// when the scheduler runs it, and singleflight offers no way to wait until every
// caller has arrived, so that assertion fails about one run in three. The
// cost guarantee that can be stated deterministically is the one below.
// Run with -race, where this is the test that would catch a data race.
func TestResolve_ConcurrentResolvesAreSafe(t *testing.T) {
	repo := &fakeRepo{saveID: uuid.New()}
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	r := NewResolver(repo, fwd, quietLogger())

	const callers = 10
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got.Lat != portoPT.Lat {
				t.Errorf("lat: got %v, want %v", got.Lat, portoPT.Lat)
			}
		}()
	}
	wg.Wait()

	calls := fwd.callCount()
	if calls < 1 {
		t.Fatal("geocoder was never called")
	}
	if saves := int64(repo.savedCount()); saves > calls {
		t.Errorf("saves (%d) exceeded geocoder calls (%d)", saves, calls)
	}
}

// A city resolved once is resolved from the database afterwards. This is the
// deterministic half of the cost guarantee: whatever the scheduler does to
// concurrent callers, the *second request* must never pay the provider again.
func TestResolve_SecondResolveUsesStoredRow(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{saveID: id}
	fwd := &fakeForward{places: []geocode.Place{portoPT}}
	r := NewResolver(repo, fwd, quietLogger())

	if _, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// The row now exists, exactly as it would on a later request.
	repo.candidates = []locitypes.CityDetail{{
		ID: id, Name: "Porto", Country: "Portugal",
		CenterLatitude: ptr(portoPT.Lat), CenterLongitude: ptr(portoPT.Lon),
	}}

	got, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got.Source != ResolvedFromDB {
		t.Errorf("source: got %q, want db", got.Source)
	}
	if fwd.callCount() != 1 {
		t.Errorf("geocoder calls: got %d, want 1 across both resolves", fwd.callCount())
	}
}

func TestResolve_NilGeocoderDegradesToDatabaseOnly(t *testing.T) {
	stored := &fakeRepo{candidates: []locitypes.CityDetail{{
		ID: uuid.New(), Name: "Porto", CenterLatitude: ptr(41.1), CenterLongitude: ptr(-8.6),
	}}}
	r := NewResolver(stored, nil, quietLogger())
	if _, err := r.Resolve(context.Background(), ResolveQuery{Name: "Porto"}); err != nil {
		t.Fatalf("stored city should still resolve: %v", err)
	}

	empty := NewResolver(&fakeRepo{}, nil, quietLogger())
	_, err := empty.Resolve(context.Background(), ResolveQuery{Name: "Porto"})
	if !errors.Is(err, ErrCityUnresolvable) {
		t.Errorf("got %v, want ErrCityUnresolvable", err)
	}
}

func TestResolve_EmptyQueryIsRejected(t *testing.T) {
	r := NewResolver(&fakeRepo{}, &fakeForward{}, quietLogger())
	_, err := r.Resolve(context.Background(), ResolveQuery{Name: "  "})
	if !errors.Is(err, ErrCityUnresolvable) {
		t.Errorf("got %v, want ErrCityUnresolvable", err)
	}
}

func TestFoldName(t *testing.T) {
	cases := map[string]string{
		"Évora":     "evora",
		"  PORTO  ": "porto",
		"São Paulo": "sao paulo",
		"Köln":      "koln",
		"Beja":      "beja",
	}
	for in, want := range cases {
		if got := foldName(in); got != want {
			t.Errorf("foldName(%q) = %q, want %q", in, got, want)
		}
	}
}
