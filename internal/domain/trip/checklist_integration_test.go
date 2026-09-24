//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newChecklistTrip(t *testing.T, userID uuid.UUID) uuid.UUID {
	t.Helper()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	saved, err := repo.SaveTrip(context.Background(), &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Checklist trip",
		Days: []TripDay{{DayNumber: 1}},
	}, 0)
	require.NoError(t, err)
	return saved.ID
}

func TestChecklistRepository_RoundTripOwnershipAndCascade(t *testing.T) {
	ctx := context.Background()
	repo := NewChecklistRepository(testTripDB)
	owner := newTripUser(t, "checklist-"+uuid.NewString()+"@loci.test")
	intruder := newTripUser(t, "checklist-intruder-"+uuid.NewString()+"@loci.test")
	tripID := newChecklistTrip(t, owner)

	item := ChecklistItem{
		ID: uuid.New(), Kind: ChecklistKindExpense, Text: "Tram pass",
		AmountMinor: 1060, Currency: "EUR", Position: 2,
	}
	first, err := repo.UpsertItem(ctx, tripID, owner, item)
	require.NoError(t, err)
	require.False(t, first.UpdatedAt.IsZero())

	item.Done = true
	second, err := repo.UpsertItem(ctx, tripID, owner, item)
	require.NoError(t, err)
	assert.False(t, second.UpdatedAt.Before(first.UpdatedAt))

	require.NoError(t, repo.DismissSuggestion(ctx, tripID, owner, "  Rain Jacket "))
	require.NoError(t, repo.DismissSuggestion(ctx, tripID, owner, "rain jacket"))

	cl, err := repo.GetChecklist(ctx, tripID, owner)
	require.NoError(t, err)
	require.Len(t, cl.Items, 1, "a replayed upsert must not duplicate")
	assert.True(t, cl.Items[0].Done)
	assert.Equal(t, int64(1060), cl.Items[0].AmountMinor)
	assert.Equal(t, "EUR", cl.Items[0].Currency)
	assert.Equal(t, []string{"rain jacket"}, cl.Dismissed)

	t.Run("another user's trip is NotFound on every call", func(t *testing.T) {
		_, err := repo.GetChecklist(ctx, tripID, intruder)
		assert.ErrorIs(t, err, ErrNotFound)
		_, err = repo.UpsertItem(ctx, tripID, intruder, ChecklistItem{ID: item.ID, Kind: ChecklistKindPacking, Text: "hijack"})
		assert.ErrorIs(t, err, ErrNotFound)
		assert.ErrorIs(t, repo.DeleteItem(ctx, tripID, intruder, item.ID), ErrNotFound)
		assert.ErrorIs(t, repo.DismissSuggestion(ctx, tripID, intruder, "x"), ErrNotFound)

		cl, err := repo.GetChecklist(ctx, tripID, owner)
		require.NoError(t, err)
		require.Len(t, cl.Items, 1)
		assert.Equal(t, "Tram pass", cl.Items[0].Text, "the intruder's upsert must not overwrite")
	})

	t.Run("delete is idempotent", func(t *testing.T) {
		require.NoError(t, repo.DeleteItem(ctx, tripID, owner, item.ID))
		require.NoError(t, repo.DeleteItem(ctx, tripID, owner, item.ID))
		cl, err := repo.GetChecklist(ctx, tripID, owner)
		require.NoError(t, err)
		assert.Empty(t, cl.Items)
	})

	t.Run("deleting the trip removes its checklist", func(t *testing.T) {
		_, err := repo.UpsertItem(ctx, tripID, owner, ChecklistItem{ID: uuid.New(), Kind: ChecklistKindPacking, Text: "Socks"})
		require.NoError(t, err)
		_, err = testTripDB.Exec(ctx, `DELETE FROM trips WHERE id = $1`, tripID)
		require.NoError(t, err)
		var n int
		require.NoError(t, testTripDB.QueryRow(ctx,
			`SELECT (SELECT COUNT(*) FROM trip_checklist_items WHERE trip_id = $1) +
			        (SELECT COUNT(*) FROM trip_packing_dismissed WHERE trip_id = $1)`, tripID).Scan(&n))
		assert.Zero(t, n)
	})
}

func TestChecklistRepository_CapHoldsUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	repo := NewChecklistRepository(testTripDB)
	owner := newTripUser(t, "checklist-cap-"+uuid.NewString()+"@loci.test")
	tripID := newChecklistTrip(t, owner)

	// Fill to one below the cap directly, then race several inserts for the
	// last slot: exactly one may win.
	_, err := testTripDB.Exec(ctx, `
		INSERT INTO trip_checklist_items (trip_id, id, user_id, kind, text)
		SELECT $1, gen_random_uuid(), $2, 1, 'item ' || g FROM generate_series(1, $3) g`,
		tripID, owner, MaxChecklistItems-1)
	require.NoError(t, err)

	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.UpsertItem(ctx, tripID, owner,
				ChecklistItem{ID: uuid.New(), Kind: ChecklistKindPacking, Text: "racer"})
		}(i)
	}
	wg.Wait()

	won := 0
	for _, e := range errs {
		if e == nil {
			won++
		} else {
			assert.ErrorIs(t, e, ErrChecklistFull)
		}
	}
	assert.Equal(t, 1, won)

	cl, err := repo.GetChecklist(ctx, tripID, owner)
	require.NoError(t, err)
	assert.Len(t, cl.Items, MaxChecklistItems)

	// Updating an existing item on a full list still works.
	existing := cl.Items[0]
	existing.Done = true
	_, err = repo.UpsertItem(ctx, tripID, owner, existing)
	require.NoError(t, err)
}
