package tripaction

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
)

// --- fakes ---

type memStore struct{ byID map[uuid.UUID]*Proposal }

func (m *memStore) Create(_ context.Context, p *Proposal) error {
	p.ID, p.Status, p.CreatedAt = uuid.New(), StatusPending, time.Now()
	cp := *p
	m.byID[p.ID] = &cp
	return nil
}

func (m *memStore) Get(_ context.Context, id, userID uuid.UUID) (*Proposal, error) {
	p, ok := m.byID[id]
	if !ok || p.UserID != userID {
		return nil, ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (m *memStore) Transition(ctx context.Context, id uuid.UUID, from, to Status) error {
	if err := ctx.Err(); err != nil {
		return err // like pgx on a dead context
	}
	p, ok := m.byID[id]
	if !ok || p.Status != from {
		return ErrNotPending
	}
	p.Status = to
	return nil
}

type fakeTrips struct{ t *trip.Trip }

func (f *fakeTrips) GetTrip(_ context.Context, id, userID uuid.UUID) (*trip.Trip, error) {
	if f.t.ID != id || f.t.UserID != userID {
		return nil, trip.ErrNotFound
	}
	cp := *f.t
	return &cp, nil
}

// fakePlans records what was applied; version moves like the real service.
type fakePlans struct {
	trips   *fakeTrips
	applied []string
	fail    error
}

func (f *fakePlans) bump(base int64, what string) (*trip.Trip, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	if f.trips.t.Version != base {
		return nil, trip.ErrVersionConflict
	}
	f.trips.t.Version++
	f.applied = append(f.applied, what)
	cp := *f.trips.t
	return &cp, nil
}

func (f *fakePlans) SetDates(_ context.Context, _, _ uuid.UUID, base int64, _, _ time.Time) (*trip.Trip, error) {
	return f.bump(base, "dates")
}

func (f *fakePlans) SetStay(_ context.Context, _, _ uuid.UUID, base int64, s trip.TripStay) (*trip.Trip, error) {
	return f.bump(base, "stay:"+s.Name)
}

func (f *fakePlans) AddFlight(_ context.Context, _, _ uuid.UUID, base int64, _ trip.TripFlight) (*trip.Trip, error) {
	return f.bump(base, "flight")
}

func (f *fakePlans) ReplaceDays(_ context.Context, _, _ uuid.UUID, base int64, _ []trip.TripDay) (*trip.Trip, error) {
	return f.bump(base, "days")
}

func (f *fakePlans) FlightLinks(q flights.Query) ([]flights.Link, error) {
	return flights.DeepLinks{}.Links(q), nil
}

type fakeHotels struct{ found []locitypes.POIDetailedInfo }

func (f fakeHotels) GetNearbyHotels(context.Context, uuid.UUID, float64, float64, float64, string, string) ([]locitypes.POIDetailedInfo, error) {
	return f.found, nil
}

type fakePlaces struct{}

func (fakePlaces) Search(context.Context, string, int) ([]geocode.Place, error) {
	return []geocode.Place{{Lat: 38.72, Lon: -9.14}}, nil
}

type fakeRegen struct {
	calls  int
	err    error
	parent uuid.UUID
	cancel context.CancelFunc
}

func (f *fakeRegen) GenerateDays(_ context.Context, _, parent uuid.UUID, _ string, _ int) ([]trip.TripDay, error) {
	f.calls++
	f.parent = parent
	if f.cancel != nil {
		f.cancel() // the client went away mid re-plan
		return nil, context.Canceled
	}
	return []trip.TripDay{{DayNumber: 1}}, f.err
}

type fakeSessions struct {
	owner  uuid.UUID
	posted []string
}

func (f *fakeSessions) GetSession(_ context.Context, id uuid.UUID) (*locitypes.ChatSession, error) {
	return &locitypes.ChatSession{ID: id, UserID: f.owner}, nil
}

func (f *fakeSessions) AddMessageToSession(_ context.Context, _ uuid.UUID, m locitypes.ConversationMessage) error {
	f.posted = append(f.posted, m.Content)
	return nil
}

type fixture struct {
	svc      *Service
	store    *memStore
	plans    *fakePlans
	regen    *fakeRegen
	sessions *fakeSessions
	uid, tid uuid.UUID
	now      time.Time
}

func newFixture(t *testing.T, reply string, hotels []locitypes.POIDetailedInfo) *fixture {
	t.Helper()
	uid, tid := uuid.New(), uuid.New()
	trips := &fakeTrips{t: &trip.Trip{ID: tid, UserID: uid, CityName: "Lisbon", Version: 3, Days: make([]trip.TripDay, 3)}}
	f := &fixture{
		store: &memStore{byID: map[uuid.UUID]*Proposal{}}, plans: &fakePlans{trips: trips},
		regen: &fakeRegen{}, sessions: &fakeSessions{owner: uid}, uid: uid, tid: tid,
		now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	f.svc = NewService(Deps{
		LLM: &scripted{replies: []string{reply, reply, reply}}, Store: f.store, Trips: trips, Plans: f.plans,
		Hotels: fakeHotels{found: hotels}, Places: fakePlaces{}, Regen: f.regen, Sessions: f.sessions,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return f.now },
	})
	return f
}

const allFour = `{"actions":[
 {"kind":"set_dates","start_date":"2026-11-12","end_date":"2026-11-17"},
 {"kind":"search_hotels","city":"Lisbon","min_stars":4,"max_stars":4},
 {"kind":"regenerate_days","days":6},
 {"kind":"search_flights","origin":{"name":"New York","iata":"JFK"},"destination":{"name":"Lisbon","iata":"LIS"},"depart":"2026-11-12","return":"2026-11-17","passengers":2}]}`

var lisbonHotels = []locitypes.POIDetailedInfo{
	{ID: uuid.New(), Name: "Pestana Palace", StarRating: "5", Rating: 4.8, Website: "https://pestana.example"},
	{ID: uuid.New(), Name: "Hotel Avenida", StarRating: "4 stars", Rating: 4.1, Website: "http://avenida.example"},
	{Name: "Generated Inn", StarRating: "4.5", Rating: 4.6},
	{ID: uuid.New(), Name: "No Stars", Rating: 4.9},
}

// --- Propose ---

func TestPropose_OneProposalPerActionWithOptions(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	sid := uuid.New()
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, sid, "the lot")
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Len(t, f.store.byID, 4, "every proposal is stored")
	require.Equal(t, sid, got[0].SessionID)
	require.Equal(t, f.now.Add(proposalTTL), got[0].ExpiresAt)

	hotels := got[1]
	require.Equal(t, KindSearchHotels, hotels.Action.Kind)
	require.Len(t, hotels.Options, 2, "4..4 keeps the 4.5 and the 4; drops the 5 and the unrated")
	require.Equal(t, "Generated Inn", hotels.Options[0].Stay.Name, "best rated first")
	require.Empty(t, hotels.Options[0].Stay.POIID, "a generated hotel has no POI id")
	require.Equal(t, "4.5", hotels.Options[0].Stay.StarRating)
	require.Nil(t, hotels.Options[1].Stay.BookingURL, "an http site is not a booking link")

	flight := got[3]
	require.Len(t, flight.Options, 1)
	require.NotEmpty(t, flight.Options[0].Flight.Links)
	require.EqualValues(t, 2, flight.Options[0].Flight.Passengers)
}

func TestPropose_NothingAskedIsNoProposals(t *testing.T) {
	f := newFixture(t, `{"actions":[]}`, nil)
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.Nil, "what's good for dinner?")
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, f.store.byID)
}

func TestPropose_SomeoneElsesTripOrSession(t *testing.T) {
	f := newFixture(t, allFour, nil)
	_, err := f.svc.Propose(context.Background(), uuid.New(), f.tid, uuid.Nil, "x")
	require.ErrorIs(t, err, trip.ErrNotFound)

	f.sessions.owner = uuid.New()
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.New(), "x")
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, got[0].SessionID, "a session the caller does not own is not used")
}

func TestPropose_NoHotelsFound(t *testing.T) {
	f := newFixture(t, `{"actions":[{"kind":"search_hotels","city":"Lisbon","min_stars":5,"max_stars":5}]}`, lisbonHotels[1:])
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.Nil, "5 stars")
	require.NoError(t, err)
	require.Empty(t, got[0].Options)
	require.Contains(t, got[0].Summary, "No 5★ hotels")
}

// --- Apply / Dismiss ---

func proposeAll(t *testing.T, f *fixture) []Proposal {
	t.Helper()
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.New(), "the lot")
	require.NoError(t, err)
	return got
}

func intp(i int) *int { return &i }

func TestApply_EachKind(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	ctx := context.Background()

	tr, msg, err := f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 3)
	require.NoError(t, err)
	require.NotNil(t, msg)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, intp(1), tr.Version)
	require.NoError(t, err)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[2].ID, nil, 5)
	require.NoError(t, err)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[3].ID, nil, 6)
	require.NoError(t, err)

	require.Equal(t, []string{"dates", "stay:Hotel Avenida", "days", "flight"}, f.plans.applied)
	require.Len(t, f.sessions.posted, 4, "each confirmation lands in the thread")
}

func TestApply_OnlyOnce(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	ctx := context.Background()
	_, _, err := f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 3)
	require.NoError(t, err)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 4)
	require.ErrorIs(t, err, ErrNotPending)

	require.NoError(t, f.svc.Dismiss(ctx, f.uid, ps[1].ID))
	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, intp(0), 4)
	require.ErrorIs(t, err, ErrNotPending, "dismissed stays dismissed")
	require.Equal(t, []string{"dates"}, f.plans.applied)
}

func TestApply_Refusals(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	ctx := context.Background()

	_, _, err := f.svc.Apply(ctx, uuid.New(), ps[0].ID, nil, 3)
	require.ErrorIs(t, err, ErrNotFound, "someone else's proposal")

	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, nil, 3)
	require.ErrorIs(t, err, ErrNoOption, "hotels need a pick")
	_, _, err = f.svc.Apply(ctx, f.uid, ps[1].ID, intp(9), 3)
	require.ErrorIs(t, err, ErrNoOption)

	f.now = f.now.Add(25 * time.Hour)
	_, _, err = f.svc.Apply(ctx, f.uid, ps[0].ID, nil, 3)
	require.ErrorIs(t, err, ErrExpired)
	require.Empty(t, f.plans.applied)
}

func TestApply_StaleRePlanNeverGenerates(t *testing.T) {
	f := newFixture(t, allFour, nil)
	ps := proposeAll(t, f)
	_, _, err := f.svc.Apply(context.Background(), f.uid, ps[2].ID, nil, 2)
	require.ErrorIs(t, err, trip.ErrVersionConflict)
	require.Zero(t, f.regen.calls, "the slow generation never runs on a stale trip")
	_, _, err = f.svc.Apply(context.Background(), f.uid, ps[2].ID, nil, 3)
	require.NoError(t, err, "the refusal gave the proposal back")
}

func TestApply_FailureGivesTheProposalBack(t *testing.T) {
	f := newFixture(t, allFour, nil)
	ps := proposeAll(t, f)
	f.regen.err = errors.New("model down")
	_, _, err := f.svc.Apply(context.Background(), f.uid, ps[2].ID, nil, 3)
	require.Error(t, err)
	require.Equal(t, StatusPending, f.store.byID[ps[2].ID].Status)
}

func TestApply_MultiCityRePlanIsRefused(t *testing.T) {
	f := newFixture(t, `{"actions":[]}`, nil)
	f.plans.trips.t.Cities = []trip.TripCity{{CityName: "Lisbon"}, {CityName: "Porto"}}
	p := &Proposal{UserID: f.uid, TripID: f.tid, Action: Action{Kind: KindRegenerateDays, Days: 4}, ExpiresAt: f.now.Add(time.Hour)}
	require.NoError(t, f.store.Create(context.Background(), p))
	_, _, err := f.svc.Apply(context.Background(), f.uid, p.ID, nil, 3)
	require.ErrorIs(t, err, trip.ErrInvalidEdit)
	require.Zero(t, f.regen.calls)
}

// A multi-city trip is never offered a re-plan card it could only refuse.
func TestPropose_NoRePlanCardOnAMultiCityTrip(t *testing.T) {
	f := newFixture(t, `{"actions":[{"kind":"regenerate_days","days":4},{"kind":"set_dates","start_date":"2026-11-12","end_date":"2026-11-15"}]}`, nil)
	f.plans.trips.t.Cities = []trip.TripCity{{CityName: "Lisbon"}, {CityName: "Porto"}}
	got, err := f.svc.Propose(context.Background(), f.uid, f.tid, uuid.Nil, "4 days, 12 to 15 Nov")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, KindSetDates, got[0].Action.Kind)
}

// The client going away mid re-plan (or the RPC deadline) must not strand
// the proposal as applied: giving it back cannot share the dead context.
func TestApply_CancelledRePlanGivesTheProposalBack(t *testing.T) {
	f := newFixture(t, allFour, nil)
	ps := proposeAll(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	f.regen.cancel = cancel
	_, _, err := f.svc.Apply(ctx, f.uid, ps[2].ID, nil, 3)
	require.Error(t, err)
	require.Equal(t, StatusPending, f.store.byID[ps[2].ID].Status)
}

// A re-plan's generated session hangs under the trip's own thread, so it
// never shows up as a search of its own in the history.
func TestApply_RePlanHangsUnderTheTripsSession(t *testing.T) {
	f := newFixture(t, allFour, nil)
	src := uuid.New()
	s := src.String()
	f.plans.trips.t.SourceSessionID = &s
	ps := proposeAll(t, f)
	_, _, err := f.svc.Apply(context.Background(), f.uid, ps[2].ID, nil, 3)
	require.NoError(t, err)
	require.Equal(t, src, f.regen.parent)
}

func TestApplyCurrent_UsesTheTripsVersionNow(t *testing.T) {
	f := newFixture(t, allFour, lisbonHotels)
	ps := proposeAll(t, f)
	f.plans.trips.t.Version = 9 // edited elsewhere since the proposal
	tr, msg, err := f.svc.ApplyCurrent(context.Background(), f.uid, ps[0].ID, nil)
	require.NoError(t, err)
	require.EqualValues(t, 10, tr.Version)
	require.NotNil(t, msg)
	_, _, err = f.svc.ApplyCurrent(context.Background(), f.uid, ps[0].ID, nil)
	require.ErrorIs(t, err, ErrNotPending, "still applied once")
	_, _, err = f.svc.ApplyCurrent(context.Background(), uuid.New(), ps[1].ID, intp(0))
	require.ErrorIs(t, err, ErrNotFound)
}
