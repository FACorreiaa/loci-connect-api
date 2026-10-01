//go:build integration

package tripaction

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func newUserAndTrip(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid := uuid.New()
	_, err := testDB.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`, uid, "ta-"+uid.String()+"@loci.test")
	require.NoError(t, err)
	var tid uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx,
		`INSERT INTO trips (user_id, city_name, title, constraints, version) VALUES ($1, 'Lisbon', 'Lisbon', '{}', 1) RETURNING id`,
		uid).Scan(&tid))
	return uid, tid
}

func TestStore_RoundTripAndOneShotTransitions(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	uid, tid := newUserAndTrip(t)

	p := &Proposal{
		UserID: uid, TripID: tid,
		Action:    Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 4, MaxStars: 4},
		Summary:   "4★ hotels in Lisbon",
		Options:   []Option{{Label: "Hotel Avenida · 4★", Stay: &trip.TripStay{CityName: "Lisbon", Name: "Hotel Avenida", StarRating: "4"}}},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.Create(ctx, p))
	require.NotEqual(t, uuid.Nil, p.ID)
	require.Equal(t, StatusPending, p.Status)

	got, err := store.Get(ctx, p.ID, uid)
	require.NoError(t, err)
	require.Equal(t, p.Action, got.Action)
	require.Equal(t, "Hotel Avenida", got.Options[0].Stay.Name)
	require.Equal(t, uuid.Nil, got.SessionID, "no session stored as NULL, read back as Nil")

	_, err = store.Get(ctx, p.ID, uuid.New())
	require.ErrorIs(t, err, ErrNotFound, "someone else's proposal")

	require.NoError(t, store.Transition(ctx, p.ID, StatusPending, StatusApplied))
	require.ErrorIs(t, store.Transition(ctx, p.ID, StatusPending, StatusApplied), ErrNotPending, "applied once")
}
