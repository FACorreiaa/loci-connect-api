//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A client built before trip plans saves the whole TripDraft it holds, which
// has no dates, stays or flights. SaveTrip must leave them in place, and its
// response must still carry them, or the editor shows them gone until reload.
func TestRepository_SaveTripKeepsThePlan(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "plan-keep-"+uuid.NewString()+"@loci.test")

	first, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon"}},
	}, 0)
	require.NoError(t, err)

	_, err = testTripDB.Exec(ctx, `UPDATE trips SET start_date = '2026-11-12', end_date = '2026-11-14' WHERE id = $1`, first.ID)
	require.NoError(t, err)
	_, err = testTripDB.Exec(ctx, `INSERT INTO trip_stays (trip_id, city_name, name, star_rating) VALUES ($1, 'Lisbon', 'Hotel Avenida', '4')`, first.ID)
	require.NoError(t, err)
	_, err = testTripDB.Exec(ctx, `
		INSERT INTO trip_flights (id, trip_id, origin_name, destination_name, destination_iata, depart_date, links)
		VALUES ($1, $2, 'New York', 'Lisbon', 'LIS', '2026-11-12', '[{"provider":"google_flights","label":"Google Flights","url":"https://www.google.com/travel/flights/search?q=x"}]')`,
		uuid.New(), first.ID)
	require.NoError(t, err)

	// An old client's save: the draft it holds has no plan at all.
	old := *first
	old.StartDate, old.EndDate, old.Stays, old.Flights = nil, nil, nil, nil
	old.Title = "Lisbon long weekend"
	saved, err := repo.SaveTrip(ctx, &old, first.Version)
	require.NoError(t, err)

	for _, got := range []*Trip{saved, mustGet(t, repo, first.ID, userID)} {
		require.NotNil(t, got.StartDate)
		require.Equal(t, "2026-11-12", got.StartDate.Format(time.DateOnly))
		require.Equal(t, "2026-11-14", got.EndDate.Format(time.DateOnly))
		require.Len(t, got.Stays, 1)
		require.Equal(t, "Hotel Avenida", got.Stays[0].Name)
		require.Len(t, got.Flights, 1)
		require.Equal(t, "LIS", got.Flights[0].Destination.IATA)
		require.Len(t, got.Flights[0].Links, 1)
	}
}

func mustGet(t *testing.T, repo Repository, id, userID uuid.UUID) *Trip {
	t.Helper()
	got, err := repo.GetTrip(context.Background(), id, userID)
	require.NoError(t, err)
	return got
}
