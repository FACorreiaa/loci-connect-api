package trip

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/placecheck"
	"github.com/FACorreiaa/loci-connect-api/pkg/pglock"
)

// tripsByID answers GetTrip for the trips it holds.
type tripsByID struct {
	Repository
	trips  map[uuid.UUID]*Trip
	loaded []uuid.UUID
}

func (f *tripsByID) GetTrip(_ context.Context, id, _ uuid.UUID) (*Trip, error) {
	f.loaded = append(f.loaded, id)
	if t, ok := f.trips[id]; ok {
		return t, nil
	}
	return nil, ErrNotFound
}

// wantingRefresher goes to the map `each` times for every plan it is given,
// for as long as it is allowed to. failAt, when set, makes that plan's first
// visit fail.
type wantingRefresher struct {
	each   int
	plans  int
	failAt int
	days   [][]placecheck.Day
}

func (f *wantingRefresher) Refresh(_ context.Context, days []placecheck.Day, allow func() bool) (int, error) {
	f.plans++
	f.days = append(f.days, days)
	n := 0
	for range f.each {
		if !allow() {
			break
		}
		n++
		if f.failAt == f.plans {
			return n, errors.New("map is down")
		}
	}
	return n, nil
}

type fakeUpcoming struct {
	refs     []TripRef
	err      error
	from, to time.Time
	limit    int
	calls    int
}

func (f *fakeUpcoming) Upcoming(_ context.Context, from, to time.Time, limit int) ([]TripRef, error) {
	f.calls++
	f.from, f.to, f.limit = from, to, limit
	return f.refs, f.err
}

type fakeCheckLock struct {
	err      error
	unlocked int
}

func (f *fakeCheckLock) TryLock(context.Context) (func(), error) {
	if f.err != nil {
		return nil, f.err
	}
	return func() { f.unlocked++ }, nil
}

type checkRig struct {
	runner   *StopCheckRunner
	repo     *tripsByID
	refresh  *wantingRefresher
	upcoming *fakeUpcoming
	lock     *fakeCheckLock
	clock    time.Time
	slept    []time.Duration
}

// newCheckRig has n trips due, each wanting two visits to the map.
func newCheckRig(n, ceiling int) *checkRig {
	r := &checkRig{
		repo: &tripsByID{trips: map[uuid.UUID]*Trip{}}, refresh: &wantingRefresher{each: 2},
		upcoming: &fakeUpcoming{}, lock: &fakeCheckLock{},
		clock: time.Date(2026, 4, 14, 15, 30, 0, 0, time.UTC),
	}
	for range n {
		ref := TripRef{ID: uuid.New(), UserID: uuid.New()}
		r.repo.trips[ref.ID] = &Trip{ID: ref.ID, UserID: ref.UserID}
		r.upcoming.refs = append(r.upcoming.refs, ref)
	}
	r.runner = NewStopCheckRunner(&Handler{repo: r.repo}, r.refresh, r.upcoming, r.lock, ceiling, nil)
	r.runner.now = func() time.Time { return r.clock }
	r.runner.sleep = func(_ context.Context, d time.Duration) { r.slept = append(r.slept, d) }
	return r
}

func TestStopCheckRunner_RefreshesTripsOfTheComingWeek(t *testing.T) {
	r := newCheckRig(2, 100)

	require.Equal(t, 4, r.runner.Tick(context.Background()))

	require.Equal(t, time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC), r.upcoming.from, "from the start of today")
	require.Equal(t, time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC), r.upcoming.to, "a week ahead")
	require.Equal(t, stopCheckBatch, r.upcoming.limit)
	require.Equal(t, 2, r.refresh.plans)
	require.Equal(t, 1, r.lock.unlocked)
	// A pause before every visit but the first.
	require.Equal(t, []time.Duration{stopCheckPause, stopCheckPause, stopCheckPause}, r.slept)
}

func TestStopCheckRunner_AnotherReplicaHoldsTheLock(t *testing.T) {
	r := newCheckRig(2, 100)
	r.lock.err = pglock.ErrHeld

	require.Zero(t, r.runner.Tick(context.Background()))
	require.Zero(t, r.upcoming.calls)
	require.Zero(t, r.refresh.plans)
}

func TestStopCheckRunner_StopsAtItsCeilingAndStartsAgainNextDay(t *testing.T) {
	r := newCheckRig(4, 3)
	ctx := context.Background()

	require.Equal(t, 3, r.runner.Tick(ctx))
	require.Len(t, r.repo.loaded, 2, "the third and fourth trips are not even read")

	// Later the same day: nothing left to spend.
	r.clock = r.clock.Add(time.Hour)
	require.Zero(t, r.runner.Tick(ctx))
	require.Len(t, r.repo.loaded, 2)

	// Past midnight UTC the count starts again.
	r.clock = r.clock.Add(12 * time.Hour)
	require.Equal(t, 3, r.runner.Tick(ctx))
}

func TestStopCheckRunner_ACeilingOfZeroNeverAsks(t *testing.T) {
	r := newCheckRig(2, 0)
	require.Zero(t, r.runner.Tick(context.Background()))
	require.Empty(t, r.repo.loaded)
}

func TestStopCheckRunner_SkipsATripItCannotReadAndSurvivesAFailedListing(t *testing.T) {
	r := newCheckRig(2, 100)
	delete(r.repo.trips, r.upcoming.refs[0].ID)
	require.Equal(t, 2, r.runner.Tick(context.Background()))
	require.Equal(t, 1, r.refresh.plans)

	r.upcoming.err = errors.New("database is away")
	require.Zero(t, r.runner.Tick(context.Background()))
}

func TestStopCheckRunner_StopsWhenCancelled(t *testing.T) {
	r := newCheckRig(3, 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Zero(t, r.runner.Tick(ctx))
	require.Empty(t, r.repo.loaded)
}

func TestStopCheckRunner_EndsTheTickWhenALookupFails(t *testing.T) {
	r := newCheckRig(4, 100)
	r.refresh.failAt = 2

	require.Equal(t, 3, r.runner.Tick(context.Background()), "two visits for the first trip, one failed for the second")
	require.Len(t, r.repo.loaded, 2, "the trips after the failure are left for the next tick")

	// The next tick starts over; the map answers again.
	r.refresh.failAt = 0
	require.Equal(t, 8, r.runner.Tick(context.Background()))
}
