//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A trip edit replaces days and stops wholesale. What the field score
// recorded on them — a finished day, a stop walked or skipped — must survive
// an edit for as long as the day or stop does, and go with it when it goes.
func TestRepository_SaveTripKeepsFinishedDaysAndStopMarks(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "fieldstate-"+uuid.NewString()+"@loci.test")

	first, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{
			{DayNumber: 1, CityName: "Lisbon", Stops: []TripStop{{Name: "Sé", OrderIndex: 0}, {Name: "Miradouro", OrderIndex: 1}}},
			{DayNumber: 2, CityName: "Lisbon", Stops: []TripStop{{Name: "Belém", OrderIndex: 0}}},
		},
	}, 0)
	require.NoError(t, err)
	day1 := first.Days[0]
	kept, dropped := day1.Stops[0].ID, day1.Stops[1].ID

	_, err = testTripDB.Exec(ctx, `UPDATE trip_days SET completed_at = NOW() WHERE id = $1`, day1.ID)
	require.NoError(t, err)
	_, err = testTripDB.Exec(ctx, `
		INSERT INTO trip_stop_marks (stop_id, trip_id, status) VALUES ($1, $3, 2), ($2, $3, 3)`, kept, dropped, first.ID)
	require.NoError(t, err)

	loaded, err := repo.GetTrip(ctx, first.ID, userID)
	require.NoError(t, err)
	require.NotNil(t, loaded.Days[0].CompletedAt)
	require.Equal(t, int16(2), loaded.Days[0].Stops[0].Status)
	require.Equal(t, int16(3), loaded.Days[0].Stops[1].Status)
	require.Equal(t, int16(0), loaded.Days[1].Stops[0].Status, "an unmarked stop reads as open")

	// Edit: day 1 keeps "Sé" and drops "Miradouro".
	edit := *loaded
	edit.Days = []TripDay{
		{ID: day1.ID, DayNumber: 1, CityName: "Lisbon", Stops: []TripStop{{ID: kept, Name: "Sé", OrderIndex: 0}}},
		{ID: first.Days[1].ID, DayNumber: 2, CityName: "Lisbon", Stops: []TripStop{{ID: first.Days[1].Stops[0].ID, Name: "Belém", OrderIndex: 0}}},
	}
	second, err := repo.SaveTrip(ctx, &edit, loaded.Version)
	require.NoError(t, err)
	require.NotNil(t, second.Days[0].CompletedAt, "a finished day stays finished across an edit")

	back, err := repo.GetTrip(ctx, first.ID, userID)
	require.NoError(t, err)
	require.NotNil(t, back.Days[0].CompletedAt)
	require.Nil(t, back.Days[1].CompletedAt)
	require.Equal(t, int16(2), back.Days[0].Stops[0].Status, "a surviving stop keeps its mark")

	var marks int
	require.NoError(t, testTripDB.QueryRow(ctx, `SELECT COUNT(*) FROM trip_stop_marks WHERE trip_id = $1`, first.ID).Scan(&marks))
	require.Equal(t, 1, marks, "the removed stop's mark goes with it")
}
