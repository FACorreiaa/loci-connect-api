package bundle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// fakeTrips hands out a new id per SaveTrip, like the real insert.
type fakeTrips struct{ saved []uuid.UUID }

func (f *fakeTrips) SaveTrip(_ context.Context, t *trip.Trip, _ int64) (*trip.Trip, error) {
	id := uuid.New()
	f.saved = append(f.saved, id)
	out := *t
	out.ID = id
	return &out, nil
}

func newClaimSvc(repo *fakeRepo, trips TripWriter) *Service {
	return NewService(repo, trips, nil, nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestClaim_SecondClaimReturnsTheSameTrip(t *testing.T) {
	repo := &fakeRepo{bundle: pack(false, StatusPublished, 2)}
	trips := &fakeTrips{}
	svc := newClaimSvc(repo, trips)
	user := uuid.New()

	first, err := svc.Claim(context.Background(), user, repo.bundle.ID)
	require.NoError(t, err)
	second, err := svc.Claim(context.Background(), user, repo.bundle.ID)
	require.NoError(t, err)

	assert.Equal(t, first, second, "a repeat claim must answer with the trip the first one wrote")
	assert.Len(t, trips.saved, 1, "a repeat claim must not write another trip")
	assert.Equal(t, 1, repo.loadDaysCall, "a repeat claim need not read the pack again")
}

func TestClaim_EachUserGetsTheirOwnTrip(t *testing.T) {
	repo := &fakeRepo{bundle: pack(false, StatusPublished, 2)}
	trips := &fakeTrips{}
	svc := newClaimSvc(repo, trips)

	a, err := svc.Claim(context.Background(), uuid.New(), repo.bundle.ID)
	require.NoError(t, err)
	b, err := svc.Claim(context.Background(), uuid.New(), repo.bundle.ID)
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	assert.Len(t, trips.saved, 2)
}

func TestClaim_ConcurrentClaimAnswersWithTheWinner(t *testing.T) {
	winner := uuid.New()
	repo := &fakeRepo{bundle: pack(false, StatusPublished, 1), raceTrip: winner}
	svc := newClaimSvc(repo, &fakeTrips{})

	got, err := svc.Claim(context.Background(), uuid.New(), repo.bundle.ID)
	require.NoError(t, err)
	assert.Equal(t, winner, got)
}

func TestClaim_UnpaidPaidPackStillRefusedBeforeLookup(t *testing.T) {
	user := uuid.New()
	repo := &fakeRepo{bundle: pack(true, StatusPublished, 2), claims: map[uuid.UUID]uuid.UUID{user: uuid.New()}}
	svc := newClaimSvc(repo, &fakeTrips{})

	_, err := svc.Claim(context.Background(), user, repo.bundle.ID)
	assert.ErrorIs(t, err, ErrNotOwned, "a refunded buyer's old claim must not bypass ownership")
}

func TestClaim_NoTripsStoreIsAConfigurationError(t *testing.T) {
	repo := &fakeRepo{bundle: pack(false, StatusPublished, 2)}
	svc := newClaimSvc(repo, nil)

	_, err := svc.Claim(context.Background(), uuid.New(), repo.bundle.ID)
	require.ErrorIs(t, err, ErrNotConfigured)
	assert.NotErrorIs(t, err, ErrNotPurchasable)

	ce := new(connect.Error)
	require.True(t, errors.As(toConnectErr(err), &ce))
	assert.Equal(t, connect.CodeUnavailable, ce.Code())
	assert.NotContains(t, ce.Message(), "not for sale")
}
