//go:build integration

package assistant

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func newPerson(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(), `INSERT INTO users (id, email) VALUES ($1, $2)`, id, "as-"+id.String()+"@loci.test")
	require.NoError(t, err)
	return id
}

func TestStore_ReserveAskStopsAtTheLimit(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	user, day := newPerson(t), time.Now().UTC().Truncate(24*time.Hour)

	for range 2 {
		ok, err := store.ReserveAsk(ctx, user, day, 2)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := store.ReserveAsk(ctx, user, day, 2)
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = store.ReserveAsk(ctx, user, day.AddDate(0, 0, 1), 2)
	require.NoError(t, err)
	require.True(t, ok, "tomorrow is a new day")
}

func TestStore_ReserveAskHoldsUnderARace(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	user, day := newPerson(t), time.Now().UTC().Truncate(24*time.Hour)

	var granted atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			ok, err := store.ReserveAsk(ctx, user, day, 3)
			require.NoError(t, err)
			if ok {
				granted.Add(1)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 3, granted.Load())
}

func TestStore_SpendAddsUpAcrossEverybody(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	// A far-off day of its own: other tests here spend today.
	day := time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC)

	a, b := newPerson(t), newPerson(t)
	require.NoError(t, store.AddSpend(ctx, a, day, Spend{InputTokens: 10, OutputTokens: 2, CostMicroUSD: 100}))
	require.NoError(t, store.AddSpend(ctx, a, day, Spend{InputTokens: 10, OutputTokens: 2, CostMicroUSD: 50}))
	require.NoError(t, store.AddSpend(ctx, b, day, Spend{CostMicroUSD: 7}))

	total, err := store.DaySpendMicroUSD(ctx, day)
	require.NoError(t, err)
	require.EqualValues(t, 157, total)
}

func TestStore_ConversationsBelongToOnePersonAndExpire(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	me, them := newPerson(t), newPerson(t)
	now := time.Now()
	turns := []Turn{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "olá"}}

	id, err := store.SaveTurns(ctx, me, nil, turns, now.Add(time.Hour))
	require.NoError(t, err)

	got, ok, err := store.LoadTurns(ctx, me, id, now)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, turns, got)

	_, ok, err = store.LoadTurns(ctx, them, id, now)
	require.NoError(t, err)
	require.False(t, ok, "somebody else's reads as none")

	_, ok, err = store.LoadTurns(ctx, me, id, now.Add(2*time.Hour))
	require.NoError(t, err)
	require.False(t, ok, "expired reads as none")

	theirs, err := store.SaveTurns(ctx, them, &id, turns[:1], now.Add(time.Hour))
	require.NoError(t, err)
	require.NotEqual(t, id, theirs, "saving onto somebody else's id starts a new one")
	got, _, err = store.LoadTurns(ctx, me, id, now)
	require.NoError(t, err)
	require.Len(t, got, 2, "and leaves theirs alone")

	same, err := store.SaveTurns(ctx, me, &id, turns[:1], now.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, id, same)
}

func TestStore_PlaceActionRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	me, them := newPerson(t), newPerson(t)
	listID, listName := uuid.New(), "Lisbon eats"

	a := &PlaceAction{
		UserID: me, Kind: placeKindList, POIID: uuid.New(), POIName: "Pastéis de Belém",
		ListID: &listID, ListName: &listName, Summary: "Add Pastéis de Belém to Lisbon eats",
		ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second),
	}
	require.NoError(t, store.CreatePlaceAction(ctx, a))
	require.NotEqual(t, uuid.Nil, a.ID)

	got, err := store.GetPlaceAction(ctx, a.ID, me)
	require.NoError(t, err)
	require.Equal(t, placeStatusPending, got.Status)
	require.Equal(t, a.POIID, got.POIID)
	require.Equal(t, listID, *got.ListID)
	require.Equal(t, listName, *got.ListName)
	require.Equal(t, a.Summary, got.Summary)
	require.True(t, a.ExpiresAt.Equal(got.ExpiresAt))

	_, err = store.GetPlaceAction(ctx, a.ID, them)
	require.ErrorIs(t, err, errNoPlaceAction)
	moved, err := store.MovePlaceAction(ctx, a.ID, them, placeStatusPending, placeStatusApplied)
	require.NoError(t, err)
	require.False(t, moved, "nobody else can move it")

	fav := &PlaceAction{
		UserID: me, Kind: placeKindFavourite, POIID: uuid.New(), POIName: "Belém Tower",
		Summary: "Save Belém Tower to your favourites", ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.CreatePlaceAction(ctx, fav))
	got, err = store.GetPlaceAction(ctx, fav.ID, me)
	require.NoError(t, err)
	require.Nil(t, got.ListID, "a favourite has no list")
	require.Nil(t, got.ListName)
}

func TestStore_OnlyOneOfARaceClaimsACard(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	me := newPerson(t)
	a := &PlaceAction{
		UserID: me, Kind: placeKindFavourite, POIID: uuid.New(), POIName: "Belém Tower",
		Summary: "Save Belém Tower to your favourites", ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.CreatePlaceAction(ctx, a))

	var claimed atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			ok, err := store.MovePlaceAction(ctx, a.ID, me, placeStatusPending, placeStatusApplied)
			require.NoError(t, err)
			if ok {
				claimed.Add(1)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, claimed.Load())

	back, err := store.MovePlaceAction(ctx, a.ID, me, placeStatusApplied, placeStatusPending)
	require.NoError(t, err)
	require.True(t, back, "a failed write gives it back")
}

func TestStore_OldCardsGoWhenNewOnesArrive(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testDB)
	me := newPerson(t)
	old := &PlaceAction{
		UserID: me, Kind: placeKindFavourite, POIID: uuid.New(), POIName: "Old",
		Summary: "Save Old to your favourites", ExpiresAt: time.Now().Add(-time.Hour),
	}
	require.NoError(t, store.CreatePlaceAction(ctx, old))
	next := &PlaceAction{
		UserID: me, Kind: placeKindFavourite, POIID: uuid.New(), POIName: "New",
		Summary: "Save New to your favourites", ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.CreatePlaceAction(ctx, next))

	_, err := store.GetPlaceAction(ctx, old.ID, me)
	require.ErrorIs(t, err, errNoPlaceAction)
	_, err = store.GetPlaceAction(ctx, next.ID, me)
	require.NoError(t, err)
}
