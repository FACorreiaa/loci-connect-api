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

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
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

func TestPlanRepository_SavePlan(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, logger)
	plans := NewPlanRepository(testTripDB, logger)
	userID := newTripUser(t, "plan-save-"+uuid.NewString()+"@loci.test")

	tr, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{
			{DayNumber: 1, CityName: "Lisbon", Stops: []TripStop{{Name: "Belém", OrderIndex: 0}}},
			{DayNumber: 2, CityName: "Lisbon"},
		},
	}, 0)
	require.NoError(t, err)
	stopID := tr.Days[0].Stops[0].ID

	require.NoError(t, applyDates(tr, day("2026-11-12"), day("2026-11-13")))
	require.NoError(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "Hotel Avenida", StarRating: "4"}))
	f, err := appendFlight(tr, TripFlight{
		Origin: flights.Place{Name: "New York", IATA: "JFK"}, Destination: flights.Place{Name: "Lisbon", IATA: "LIS"},
		DepartDate: day("2026-11-12"), Links: []flights.Link{{Provider: "google_flights", Label: "Google Flights", URL: "https://example.test"}},
	})
	require.NoError(t, err)

	saved, err := plans.SavePlan(ctx, tr, tr.Version)
	require.NoError(t, err)
	require.Equal(t, tr.Version, saved.Version)
	require.Equal(t, "2026-11-13", saved.Days[1].Date.Format(time.DateOnly))
	require.Equal(t, stopID, saved.Days[0].Stops[0].ID, "only dates move; stops keep their ids")
	require.Len(t, saved.Stays, 1)
	require.Len(t, saved.Flights, 1)
	require.Equal(t, f.ID, saved.Flights[0].ID, "flight ids survive a save")
	require.Equal(t, "https://example.test", saved.Flights[0].Links[0].URL)

	// A save from a stale copy is refused and changes nothing.
	stale := *saved
	stale.Stays = nil
	_, err = plans.SavePlan(ctx, &stale, saved.Version-1)
	require.ErrorIs(t, err, ErrVersionConflict)
	require.Len(t, mustGet(t, repo, tr.ID, userID).Stays, 1)

	// Someone else's trip is not found, not overwritten.
	other := *saved
	other.UserID = newTripUser(t, "plan-other-"+uuid.NewString()+"@loci.test")
	_, err = plans.SavePlan(ctx, &other, saved.Version)
	require.ErrorIs(t, err, ErrNotFound)
}

// The calendar's "Pin dates" (web and iOS) re-dates a trip by sending its
// days back through SaveTrip. The trip's start and end must move with them,
// or the editor and the calendar show two different trips.
func TestRepository_SaveTripMovesPlanDatesWithDayOne(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, logger)
	plans := NewPlanRepository(testTripDB, logger)
	userID := newTripUser(t, "plan-pin-"+uuid.NewString()+"@loci.test")

	tr, err := repo.SaveTrip(ctx, &Trip{
		UserID: userID, CityName: "Lisbon", Title: "Lisbon",
		Days: []TripDay{{DayNumber: 1, CityName: "Lisbon"}, {DayNumber: 2, CityName: "Lisbon"}, {DayNumber: 3, CityName: "Lisbon"}},
	}, 0)
	require.NoError(t, err)
	require.NoError(t, applyDates(tr, day("2026-11-12"), day("2026-11-15")))
	dated, err := plans.SavePlan(ctx, tr, tr.Version)
	require.NoError(t, err)

	pinned := *dated
	pinned.Days = append([]TripDay(nil), dated.Days...)
	for i := range pinned.Days {
		d := day("2026-12-01").AddDate(0, 0, int(pinned.Days[i].DayNumber)-1)
		pinned.Days[i].Date = &d
	}
	saved, err := repo.SaveTrip(ctx, &pinned, dated.Version)
	require.NoError(t, err)

	for _, got := range []*Trip{saved, mustGet(t, repo, tr.ID, userID)} {
		require.Equal(t, "2026-12-01", got.StartDate.Format(time.DateOnly))
		require.Equal(t, "2026-12-04", got.EndDate.Format(time.DateOnly), "the span is kept")
		require.Equal(t, "2026-12-03", got.Days[2].Date.Format(time.DateOnly))
	}

	// Day dates the client hands back unchanged move nothing.
	again, err := repo.SaveTrip(ctx, saved, saved.Version)
	require.NoError(t, err)
	require.Equal(t, "2026-12-01", again.StartDate.Format(time.DateOnly))
}

func TestRepository_LatestForSession(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, logger)
	lookup := NewSessionTrips(testTripDB, logger)
	userID := newTripUser(t, "session-trip-"+uuid.NewString()+"@loci.test")
	session := uuid.New()
	s := session.String()

	saved, err := repo.SaveTrip(ctx, &Trip{UserID: userID, CityName: "Lisbon", Title: "Lisbon", SourceSessionID: &s}, 0)
	require.NoError(t, err)

	got, err := lookup.LatestForSession(ctx, userID, session)
	require.NoError(t, err)
	require.Equal(t, saved.ID, got.ID)

	_, err = lookup.LatestForSession(ctx, userID, uuid.New())
	require.ErrorIs(t, err, ErrNotFound, "a conversation that produced no trip")
	_, err = lookup.LatestForSession(ctx, newTripUser(t, "other-"+uuid.NewString()+"@loci.test"), session)
	require.ErrorIs(t, err, ErrNotFound, "someone else's trip")
}
