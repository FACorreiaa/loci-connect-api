package recap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo keeps recaps in memory and serves facts from a map.
type fakeRepo struct {
	mu         sync.Mutex
	rows       map[uuid.UUID]*Row
	candidates []Candidate
	facts      map[uuid.UUID]*TripFacts
	factsErr   error
	tz         string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]*Row{}, facts: map[uuid.UUID]*TripFacts{}, tz: "UTC"}
}

func (f *fakeRepo) byTrip(tripID uuid.UUID) *Row {
	for _, r := range f.rows {
		if r.Kind == KindTrip && r.TripID == tripID {
			return r
		}
	}
	return nil
}

func (f *fakeRepo) Candidates(_ context.Context, _, _ time.Time, _ int) ([]Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Candidate
	for _, c := range f.candidates {
		if f.byTrip(c.TripID) == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeRepo) TripCandidate(_ context.Context, userID, tripID uuid.UUID) (Candidate, error) {
	for _, c := range f.candidates {
		if c.TripID == tripID && c.UserID == userID {
			return c, nil
		}
	}
	return Candidate{}, ErrNotFound
}

func (f *fakeRepo) Queue(_ context.Context, c Candidate, start, end time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byTrip(c.TripID) != nil {
		return false, nil
	}
	id := uuid.New()
	f.rows[id] = &Row{ID: id, UserID: c.UserID, Kind: KindTrip, TripID: c.TripID, PeriodStart: start, PeriodEnd: end, Status: StatusPending}
	return true, nil
}

func (f *fakeRepo) Pending(_ context.Context, now time.Time, _ int) ([]Row, error) {
	var out []Row
	for _, r := range f.rows {
		if r.Status == StatusPending {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRepo) SaveBuilt(_ context.Context, id uuid.UUID, status Status, payload []byte, reason string) error {
	r := f.rows[id]
	if r.ShareCode != "" {
		return nil
	}
	r.Status, r.Payload, r.IneligibleReason = status, payload, reason
	if status == StatusReady && r.ReadyAt == nil {
		r.ReadyAt = ptr(time.Now())
	}
	return nil
}

func (f *fakeRepo) SaveFailure(_ context.Context, id uuid.UUID, _ string, _ time.Time, failed bool) error {
	r := f.rows[id]
	r.Attempts++
	if failed {
		r.Status = StatusFailed
	}
	return nil
}

func (f *fakeRepo) UpsertTrip(_ context.Context, userID, tripID uuid.UUID, start, end time.Time, status Status, payload []byte, reason string) (Row, error) {
	r := f.byTrip(tripID)
	if r == nil {
		id := uuid.New()
		r = &Row{ID: id, UserID: userID, Kind: KindTrip, TripID: tripID}
		f.rows[id] = r
	} else if r.ShareCode != "" {
		return *r, nil
	}
	r.PeriodStart, r.PeriodEnd, r.Status, r.Payload, r.IneligibleReason = start, end, status, payload, reason
	if status == StatusReady && r.ReadyAt == nil {
		r.ReadyAt = ptr(time.Now())
	}
	if r.NotifiedAt == nil {
		r.NotifiedAt = ptr(time.Now())
	}
	return *r, nil
}

func (f *fakeRepo) ByID(_ context.Context, userID, id uuid.UUID) (Row, error) {
	if r, ok := f.rows[id]; ok && r.UserID == userID {
		return *r, nil
	}
	return Row{}, ErrNotFound
}

func (f *fakeRepo) ByTrip(_ context.Context, userID, tripID uuid.UUID) (Row, error) {
	if r := f.byTrip(tripID); r != nil && r.UserID == userID {
		return *r, nil
	}
	return Row{}, ErrNotFound
}

func (f *fakeRepo) ByShareCode(_ context.Context, code string, _ bool) (Row, error) {
	for _, r := range f.rows {
		if r.ShareCode == code && r.Status == StatusReady {
			return *r, nil
		}
	}
	return Row{}, ErrNotFound
}

func (f *fakeRepo) List(_ context.Context, userID uuid.UUID, _ int, _ time.Time) ([]Row, error) {
	var out []Row
	for _, r := range f.rows {
		if r.UserID == userID && r.Status == StatusReady {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRepo) Unnotified(_ context.Context, _ int) ([]Notice, error) {
	var out []Notice
	for _, r := range f.rows {
		if r.Status == StatusReady && r.NotifiedAt == nil {
			out = append(out, Notice{RecapID: r.ID, UserID: r.UserID, TripID: r.TripID, Timezone: f.tz, Viewed: r.ViewedAt != nil, Payload: r.Payload})
		}
	}
	return out, nil
}

func (f *fakeRepo) ClaimNotify(_ context.Context, id uuid.UUID) (bool, error) {
	r := f.rows[id]
	if r.NotifiedAt != nil {
		return false, nil
	}
	r.NotifiedAt = ptr(time.Now())
	return true, nil
}

func (f *fakeRepo) Share(_ context.Context, userID, id uuid.UUID, code string) (string, error) {
	r, ok := f.rows[id]
	if !ok || r.UserID != userID || r.Status != StatusReady {
		return "", ErrNotFound
	}
	if r.ShareCode == "" {
		r.ShareCode = code
	}
	return r.ShareCode, nil
}

func (f *fakeRepo) Unshare(_ context.Context, userID, id uuid.UUID) error {
	r, ok := f.rows[id]
	if !ok || r.UserID != userID {
		return ErrNotFound
	}
	r.ShareCode = ""
	return nil
}

func (f *fakeRepo) MarkViewed(_ context.Context, userID, id uuid.UUID) error {
	r, ok := f.rows[id]
	if !ok || r.UserID != userID {
		return ErrNotFound
	}
	r.ViewedAt = ptr(time.Now())
	return nil
}

func (f *fakeRepo) LoadTripFacts(_ context.Context, _, tripID uuid.UUID, start, end time.Time) (*TripFacts, error) {
	if f.factsErr != nil {
		return nil, f.factsErr
	}
	tf, ok := f.facts[tripID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *tf
	cp.PeriodStart, cp.PeriodEnd = start, end
	return &cp, nil
}

type sent struct {
	to, trip    uuid.UUID
	title, body string
}

type fakeNotifier struct{ sent []sent }

func (n *fakeNotifier) RecapReady(_ context.Context, to, tripID uuid.UUID, title, body string) {
	n.sent = append(n.sent, sent{to, tripID, title, body})
}

func setup(t *testing.T, now time.Time) (*Service, *fakeRepo, *fakeNotifier, *TripFacts) {
	t.Helper()
	repo := newFakeRepo()
	n := &fakeNotifier{}
	svc := NewService(repo, "https://lociai.fyi/", n, nil)
	svc.now = func() time.Time { return now }
	f := lisbon()
	repo.facts[f.TripID] = f
	repo.candidates = []Candidate{{
		TripID: f.TripID, UserID: f.UserID, Timezone: "UTC",
		StartDate: ptr(day("2026-10-11")), EndDate: ptr(day("2026-10-15")),
	}}
	return svc, repo, n, f
}

var noon = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func TestTickQueuesBuildsAndPushesOnce(t *testing.T) {
	svc, repo, n, f := setup(t, noon)
	ctx := context.Background()

	res, err := svc.Tick(ctx)
	require.NoError(t, err)
	assert.Equal(t, TickResult{Queued: 1, Built: 1, Notified: 1}, res)
	require.Len(t, n.sent, 1)
	assert.Equal(t, f.UserID, n.sent[0].to)
	assert.Equal(t, f.TripID, n.sent[0].trip)
	assert.Equal(t, "Your Lisbon recap is ready", n.sent[0].title)
	assert.Contains(t, n.sent[0].body, "4 places")

	res, err = svc.Tick(ctx)
	require.NoError(t, err)
	assert.Equal(t, TickResult{}, res, "nothing twice")
	assert.Len(t, n.sent, 1)

	row := repo.byTrip(f.TripID)
	assert.Equal(t, StatusReady, row.Status)
}

func TestTickWaitsForPoliteHour(t *testing.T) {
	night := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	svc, _, n, _ := setup(t, night)

	res, err := svc.Tick(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Built)
	assert.Zero(t, res.Notified)
	assert.Empty(t, n.sent)

	svc.now = func() time.Time { return noon }
	res, err = svc.Tick(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Notified)
}

func TestTickSkipsPushWhenAlreadyViewed(t *testing.T) {
	night := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	svc, repo, n, f := setup(t, night)
	_, err := svc.Tick(context.Background())
	require.NoError(t, err)
	repo.byTrip(f.TripID).ViewedAt = ptr(night)

	_, err = svc.Tick(context.Background())
	require.NoError(t, err)
	assert.Empty(t, n.sent)
	assert.NotNil(t, repo.byTrip(f.TripID).NotifiedAt, "closed quietly")
}

func TestTickIneligibleIsNotPushed(t *testing.T) {
	svc, repo, n, f := setup(t, noon)
	empty := &TripFacts{TripID: f.TripID, Cities: []City{{Name: "Lisbon"}}}
	repo.facts[f.TripID] = empty

	res, err := svc.Tick(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Ineligible)
	assert.Empty(t, n.sent)
	assert.Equal(t, ReasonNoActivity, repo.byTrip(f.TripID).IneligibleReason)
}

func TestTickRetriesThenFails(t *testing.T) {
	svc, repo, _, f := setup(t, noon)
	repo.factsErr = errors.New("db down")

	for i := 0; i < MaxAttempts; i++ {
		_, err := svc.Tick(context.Background())
		require.NoError(t, err)
	}
	row := repo.byTrip(f.TripID)
	assert.Equal(t, MaxAttempts, row.Attempts)
	assert.Equal(t, StatusFailed, row.Status)
}

func TestTripRecapOnRequest(t *testing.T) {
	svc, repo, n, f := setup(t, noon)
	ctx := context.Background()

	rec, err := svc.TripRecap(ctx, f.UserID, f.TripID, false)
	require.NoError(t, err)
	assert.Equal(t, recapv1.RecapStatus_RECAP_STATUS_READY, rec.GetStatus())
	assert.True(t, rec.GetIsOwner())
	assert.True(t, rec.GetRefreshable())
	assert.NotEmpty(t, rec.GetCards())

	// Asked for it: the runner must not push it afterwards.
	_, err = svc.Tick(ctx)
	require.NoError(t, err)
	assert.Empty(t, n.sent)

	// Someone else's trip.
	_, err = svc.TripRecap(ctx, uuid.New(), f.TripID, false)
	assert.ErrorIs(t, err, ErrNotFound)

	// A rebuild picks up new facts.
	repo.facts[f.TripID].Title = "Renamed"
	rec, err = svc.TripRecap(ctx, f.UserID, f.TripID, false)
	require.NoError(t, err)
	assert.Equal(t, "Lisbon", rec.GetTitle(), "no refresh asked: the snapshot stands")
	rec, err = svc.TripRecap(ctx, f.UserID, f.TripID, true)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", rec.GetTitle())
}

func TestTripRecapNotEnded(t *testing.T) {
	svc, repo, _, f := setup(t, noon)
	repo.candidates[0].EndDate = ptr(day("2026-10-30"))

	rec, err := svc.TripRecap(context.Background(), f.UserID, f.TripID, false)
	require.NoError(t, err)
	assert.Equal(t, recapv1.RecapStatus_RECAP_STATUS_INELIGIBLE, rec.GetStatus())
	assert.Equal(t, ReasonNotEnded, rec.GetIneligibleReason())
	assert.Nil(t, repo.byTrip(f.TripID), "nothing stored for a trip under way")
}

func TestShareFreezesAndUnshareRevokes(t *testing.T) {
	svc, repo, _, f := setup(t, noon)
	ctx := context.Background()
	rec, err := svc.TripRecap(ctx, f.UserID, f.TripID, false)
	require.NoError(t, err)
	id := uuid.MustParse(rec.GetId())

	code, url, err := svc.Share(ctx, f.UserID, id)
	require.NoError(t, err)
	assert.Len(t, code, 12)
	assert.Equal(t, "https://lociai.fyi/r/"+code, url)

	again, _, err := svc.Share(ctx, f.UserID, id)
	require.NoError(t, err)
	assert.Equal(t, code, again, "sharing twice keeps the link")

	pub, err := svc.Shared(ctx, code, true)
	require.NoError(t, err)
	assert.False(t, pub.GetIsOwner())
	assert.Nil(t, card(pub, CardHighlights))

	// Frozen: a refresh changes nothing.
	repo.facts[f.TripID].Title = "Renamed"
	rec, err = svc.TripRecap(ctx, f.UserID, f.TripID, true)
	require.NoError(t, err)
	assert.Equal(t, "Lisbon", rec.GetTitle())
	assert.False(t, rec.GetRefreshable())

	_, _, err = svc.Share(ctx, uuid.New(), id)
	assert.ErrorIs(t, err, ErrNotFound, "not yours")

	require.NoError(t, svc.Unshare(ctx, f.UserID, id))
	_, err = svc.Shared(ctx, code, true)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestShareIneligibleIsNotReady(t *testing.T) {
	svc, repo, _, f := setup(t, noon)
	repo.facts[f.TripID] = &TripFacts{TripID: f.TripID}
	rec, err := svc.TripRecap(context.Background(), f.UserID, f.TripID, false)
	require.NoError(t, err)
	assert.Equal(t, recapv1.RecapStatus_RECAP_STATUS_INELIGIBLE, rec.GetStatus())

	_, _, err = svc.Share(context.Background(), f.UserID, uuid.MustParse(rec.GetId()))
	assert.ErrorIs(t, err, ErrNotReady)
}

func TestPushText(t *testing.T) {
	title, body := PushText(&recapv1.Recap{Title: "Porto"})
	assert.Equal(t, "Your Porto recap is ready", title)
	assert.Equal(t, "Tap to relive the trip.", body)
}
