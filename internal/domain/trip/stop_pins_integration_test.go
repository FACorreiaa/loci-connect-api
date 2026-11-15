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

func f64(v float64) *float64 { return &v }
func str(v string) *string   { return &v }

// A place pasted from a map link has no POI behind it, so its position lives
// on the stop. It has to survive every save, including one from a client
// built before stops had pins, which sends the stop back without it.
func TestRepository_StopPinsSurviveSaves(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "pins-"+uuid.NewString()+"@loci.test")

	first, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon", Stops: []TripStop{
			{Name: "Pasted cafe", Lat: f64(38.7101), Lon: f64(-9.1402), Address: str("Rua X 1")},
			{Name: "No pin", OrderIndex: 1},
		}}},
	}, 0)
	require.NoError(t, err)

	read, err := repo.GetTrip(ctx, first.ID, userID)
	require.NoError(t, err)
	pasted, bare := read.Days[0].Stops[0], read.Days[0].Stops[1]
	require.InDelta(t, 38.7101, *pasted.Lat, 1e-9)
	require.InDelta(t, -9.1402, *pasted.Lon, 1e-9)
	require.Equal(t, "Rua X 1", *pasted.Address)
	require.Nil(t, bare.Lat, "a stop saved without a pin has none")

	// An old client renames the stop and saves the whole trip, with no pin
	// fields at all.
	old := *read
	old.Days = []TripDay{{ID: read.Days[0].ID, DayNumber: 1, CityName: "Lisbon", Stops: []TripStop{
		{ID: pasted.ID, Name: "Pasted cafe (renamed)"},
		{ID: bare.ID, Name: "No pin", OrderIndex: 1},
	}}}
	second, err := repo.SaveTrip(ctx, &old, read.Version)
	require.NoError(t, err)

	read, err = repo.GetTrip(ctx, second.ID, userID)
	require.NoError(t, err)
	kept := read.Days[0].Stops[0]
	require.Equal(t, "Pasted cafe (renamed)", kept.Name)
	require.NotNil(t, kept.Lat, "the pin outlives a save that did not send it")
	require.InDelta(t, 38.7101, *kept.Lat, 1e-9)
	require.Equal(t, "Rua X 1", *kept.Address)
	require.Nil(t, read.Days[0].Stops[1].Lat)

	// Replacing the place under the same stop id is a different place: it
	// must not inherit the old one's position.
	swap := *read
	swap.Days = []TripDay{{ID: read.Days[0].ID, DayNumber: 1, CityName: "Lisbon", Stops: []TripStop{
		{ID: kept.ID, Name: "A real POI", POIID: uuid.NewString()},
	}}}
	third, err := repo.SaveTrip(ctx, &swap, read.Version)
	require.NoError(t, err)
	read, err = repo.GetTrip(ctx, third.ID, userID)
	require.NoError(t, err)
	require.Nil(t, read.Days[0].Stops[0].Lat, "a replaced stop does not keep the old pin")
}

// The itinerary page knows its chat session, not its trip. The lookup finds
// the caller's trip for that session, for a single city and for any city of a
// multi-city route, and never someone else's.
func TestRepository_LatestForSessionFindsACityOfARoute(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, logger)
	lookup := NewSessionTrips(testTripDB, logger)
	owner := newTripUser(t, "sess-"+uuid.NewString()+"@loci.test")
	stranger := newTripUser(t, "sess-"+uuid.NewString()+"@loci.test")

	session := uuid.New()
	sid := session.String()
	single, err := repo.SaveTrip(ctx, &Trip{
		UserID: owner, CityName: "Porto", Title: "Porto", SourceSessionID: &sid,
		Days: []TripDay{{DayNumber: 1, CityName: "Porto", Stops: []TripStop{{Name: "Ribeira"}}}},
	}, 0)
	require.NoError(t, err)

	got, err := lookup.LatestForSession(ctx, owner, session)
	require.NoError(t, err)
	require.Equal(t, single.ID, got.ID)
	require.Len(t, got.Days, 1)

	_, err = lookup.LatestForSession(ctx, stranger, session)
	require.ErrorIs(t, err, ErrNotFound, "another traveller's session finds nothing")
	_, err = lookup.LatestForSession(ctx, owner, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)

	// A multi-city route: the trip was built from a parent session, and each
	// city has its own. Opening any city finds the route's trip.
	parent, child := uuid.New(), uuid.New()
	pid := parent.String()
	route, err := repo.SaveTrip(ctx, &Trip{
		UserID: owner, CityName: "Lisbon", Title: "Lisbon and Porto", SourceSessionID: &pid,
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon"}, {DayNumber: 2, CityName: "Porto"}},
	}, 0)
	require.NoError(t, err)
	_, err = testTripDB.Exec(ctx, `
		INSERT INTO trip_cities (trip_id, order_index, city_name, nights, session_id)
		VALUES ($1, 1, 'Porto', 1, $2)`, route.ID, child)
	require.NoError(t, err)

	got, err = lookup.LatestForSession(ctx, owner, child)
	require.NoError(t, err)
	require.Equal(t, route.ID, got.ID)
	_, err = lookup.LatestForSession(ctx, stranger, child)
	require.ErrorIs(t, err, ErrNotFound)
}
