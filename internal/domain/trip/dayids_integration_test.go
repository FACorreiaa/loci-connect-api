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

// Saving a trip used to delete and re-insert every day and stop, so each save
// handed out new ids. Web's AddToTrip re-fetched the trip and still added to a
// day id that had just been replaced, and iOS's trip-day reminders are keyed
// by stop id. Ids the client sends back are kept; ids it never owned are not
// trusted and get fresh ones.
func TestRepository_SaveTripKeepsDayAndStopIDs(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "dayids-"+uuid.NewString()+"@loci.test")

	first, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Porto", Title: "Porto",
		Days: []TripDay{
			{DayNumber: 1, CityName: "Porto", Stops: []TripStop{{Name: "Livraria Lello", OrderIndex: 0}}},
			{DayNumber: 2, CityName: "Porto", Stops: []TripStop{{Name: "Ribeira", OrderIndex: 0}}},
		},
	}, 0)
	require.NoError(t, err)
	day1, day2 := first.Days[0].ID, first.Days[1].ID
	stop1 := first.Days[0].Stops[0].ID
	require.NotEqual(t, uuid.Nil, day1)
	require.NotEqual(t, uuid.Nil, stop1)

	// Edit: keep both days (with their ids), add a stop to day 1, add a third day.
	edit := *first
	edit.Days = []TripDay{
		{ID: day1, DayNumber: 1, CityName: "Porto", Stops: []TripStop{
			{ID: stop1, Name: "Livraria Lello", OrderIndex: 0},
			{Name: "Clérigos", OrderIndex: 1},
		}},
		{ID: day2, DayNumber: 2, CityName: "Porto", Stops: []TripStop{{ID: first.Days[1].Stops[0].ID, Name: "Ribeira", OrderIndex: 0}}},
		{DayNumber: 3, CityName: "Porto"},
	}
	second, err := repo.SaveTrip(ctx, &edit, first.Version)
	require.NoError(t, err)
	require.Equal(t, day1, second.Days[0].ID, "day 1 keeps its id")
	require.Equal(t, day2, second.Days[1].ID, "day 2 keeps its id")
	require.Equal(t, stop1, second.Days[0].Stops[0].ID, "an existing stop keeps its id")
	require.NotEqual(t, uuid.Nil, second.Days[0].Stops[1].ID)
	require.NotEqual(t, uuid.Nil, second.Days[2].ID, "a new day gets an id")
	require.NotContains(t, []uuid.UUID{day1, day2}, second.Days[2].ID)

	// What GetTrip reads back agrees.
	back, err := repo.GetTrip(ctx, first.ID, userID)
	require.NoError(t, err)
	require.Equal(t, day1, back.Days[0].ID)
	require.Equal(t, stop1, back.Days[0].Stops[0].ID)

	// An id this trip never owned (another trip's day) is not honoured: the
	// save succeeds and the day gets its own id.
	other, err := repo.SaveTrip(ctx, &Trip{
		UserID: newTripUser(t, "dayids-other-"+uuid.NewString()+"@loci.test"), CityName: "Braga", Title: "Braga",
		Days: []TripDay{{DayNumber: 1, CityName: "Braga"}},
	}, 0)
	require.NoError(t, err)
	foreign := other.Days[0].ID
	edit2 := *second
	edit2.Days = []TripDay{{ID: foreign, DayNumber: 1, CityName: "Porto"}}
	third, err := repo.SaveTrip(ctx, &edit2, second.Version)
	require.NoError(t, err)
	require.NotEqual(t, foreign, third.Days[0].ID, "a foreign id is replaced")
	require.NotEqual(t, uuid.Nil, third.Days[0].ID)
	// And the other trip still has its day.
	otherBack, err := repo.GetTrip(ctx, other.ID, other.UserID)
	require.NoError(t, err)
	require.Equal(t, foreign, otherBack.Days[0].ID)
}
