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

// Which trips the runner is given: those with a day inside the window, the
// one that starts soonest first, each once however many of its days fall in
// it. A trip with no dates is never one of them.
func TestUpcomingTrips_ListsTripsWithADayInTheWindowSoonestFirst(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "upcoming-"+uuid.NewString()+"@loci.test")

	// A week no other test plans a trip in.
	from := time.Date(2091, 3, 5, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	save := func(title string, dates ...*time.Time) uuid.UUID {
		t.Helper()
		days := make([]TripDay, len(dates))
		for i, d := range dates {
			days[i] = TripDay{DayNumber: int32(i + 1), Date: d, Stops: []TripStop{{Name: "Stop"}}}
		}
		saved, err := repo.SaveTrip(ctx, &Trip{UserID: userID, CityName: "Porto", Title: title, Days: days}, 0)
		require.NoError(t, err)
		return saved.ID
	}
	on := func(offset int) *time.Time {
		d := from.AddDate(0, 0, offset)
		return &d
	}

	later := save("starts on the fourth day, two days in the window", on(3), on(4))
	sooner := save("already under way", on(-2), on(-1), on(0))
	save("ended yesterday", on(-3), on(-1))
	save("starts the day after the window", on(7))
	save("no dates", nil, nil)

	got, err := NewUpcomingTrips(testTripDB).Upcoming(ctx, from, to, 10)
	require.NoError(t, err)
	require.Equal(t, []TripRef{{ID: sooner, UserID: userID}, {ID: later, UserID: userID}}, got)

	one, err := NewUpcomingTrips(testTripDB).Upcoming(ctx, from, to, 1)
	require.NoError(t, err)
	require.Equal(t, []TripRef{{ID: sooner, UserID: userID}}, one)
}
