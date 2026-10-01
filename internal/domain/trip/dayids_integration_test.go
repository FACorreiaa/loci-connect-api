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

// Legs used to be deleted and re-inserted on every save, so the globe's arc
// ids (GlobeArc.id is the trip_legs id) changed whenever a trip was edited.
// A leg kept across a save keeps its id, whether the client sends the id back
// or just the same hop; only removed legs lose theirs.
func TestRepository_SaveTripKeepsLegIDs(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "legids-"+uuid.NewString()+"@loci.test")

	lat, lon := 41.15, -8.61
	first, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Porto", Title: "Portugal",
		Days: []TripDay{{DayNumber: 1, CityName: "Porto"}, {DayNumber: 2, CityName: "Lisbon"}, {DayNumber: 3, CityName: "Faro"}},
		Legs: []TripLeg{
			{AfterDay: 1, FromName: "Porto", ToName: "Lisbon", FromLat: &lat, FromLon: &lon, Mode: "rail"},
			{AfterDay: 2, FromName: "Lisbon", ToName: "Faro", Mode: "drive"},
		},
	}, 0)
	require.NoError(t, err)
	require.Len(t, first.Legs, 2)
	portoLisbon, lisbonFaro := first.Legs[0].ID, first.Legs[1].ID
	require.NotEqual(t, uuid.Nil, portoLisbon)

	// Edit: the first leg comes back with its id, the second without an id
	// (as the multi-city planner rebuilds it), and a new leg is added.
	edit := *first
	edit.Legs = []TripLeg{
		{ID: portoLisbon, AfterDay: 1, FromName: "Porto", ToName: "Lisbon", Mode: "drive"},
		{AfterDay: 2, FromName: "Lisbon", ToName: "Faro", Mode: "drive", DurationMins: 170},
		{AfterDay: 3, FromName: "Faro", ToName: "Seville", Mode: "bus"},
	}
	second, err := repo.SaveTrip(ctx, &edit, first.Version)
	require.NoError(t, err)
	require.Equal(t, portoLisbon, second.Legs[0].ID, "a leg sent back with its id keeps it")
	require.Equal(t, lisbonFaro, second.Legs[1].ID, "a leg on the same hop keeps its id")
	require.NotContains(t, []uuid.UUID{uuid.Nil, portoLisbon, lisbonFaro}, second.Legs[2].ID)

	back, err := repo.GetTrip(ctx, first.ID, userID)
	require.NoError(t, err)
	got := map[uuid.UUID]TripLeg{}
	for _, l := range back.Legs {
		got[l.ID] = l
	}
	require.Len(t, got, 3)
	require.Equal(t, "drive", got[portoLisbon].Mode, "the kept id carries the edited row")
	require.Equal(t, int32(170), got[lisbonFaro].DurationMins)

	// Remove the middle leg: only it loses its id.
	edit2 := *second
	edit2.Legs = []TripLeg{second.Legs[0], second.Legs[2]}
	third, err := repo.SaveTrip(ctx, &edit2, second.Version)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{portoLisbon, second.Legs[2].ID}, []uuid.UUID{third.Legs[0].ID, third.Legs[1].ID})
	back, err = repo.GetTrip(ctx, first.ID, userID)
	require.NoError(t, err)
	require.Len(t, back.Legs, 2)
}
