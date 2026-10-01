package trip

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// memTrips is an in-memory Repository + PlanRepository for service tests.
type memTrips struct {
	trip  *Trip
	saves int
}

func (m *memTrips) GetTrip(_ context.Context, id, userID uuid.UUID) (*Trip, error) {
	if m.trip == nil || m.trip.ID != id || m.trip.UserID != userID {
		return nil, ErrNotFound
	}
	cp := *m.trip
	cp.Days = append([]TripDay(nil), m.trip.Days...)
	cp.Stays = append([]TripStay(nil), m.trip.Stays...)
	cp.Flights = append([]TripFlight(nil), m.trip.Flights...)
	return &cp, nil
}

func (m *memTrips) ListTrips(context.Context, uuid.UUID, int, int) ([]*Trip, int, error) {
	return nil, 0, nil
}

func (m *memTrips) SaveTrip(_ context.Context, t *Trip, base int64) (*Trip, error) {
	if m.trip.Version != base {
		return nil, ErrVersionConflict
	}
	m.saves++
	t.Version = base + 1
	m.trip = t
	return t, nil
}

func (m *memTrips) SetShare(context.Context, uuid.UUID, uuid.UUID, bool, string) (*Trip, error) {
	panic("not used")
}

func (m *memTrips) SavePlan(_ context.Context, t *Trip, base int64) (*Trip, error) {
	if m.trip.Version != base {
		return nil, ErrVersionConflict
	}
	m.saves++
	t.Version = base + 1
	m.trip = t
	return t, nil
}

func newServiceFixture() (*Service, *memTrips, uuid.UUID, uuid.UUID) {
	uid, tid := uuid.New(), uuid.New()
	mem := &memTrips{trip: &Trip{
		ID: tid, UserID: uid, CityName: "Lisbon", Version: 3,
		Days: []TripDay{{DayNumber: 1}, {DayNumber: 2}},
	}}
	return NewService(mem, mem, flights.DeepLinks{}), mem, uid, tid
}

func TestService_StaleVersionSavesNothing(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	_, err := svc.SetDates(context.Background(), uid, tid, 2, day("2026-11-12"), day("2026-11-13"))
	require.ErrorIs(t, err, ErrVersionConflict)
	require.Zero(t, mem.saves)
}

func TestService_InvalidEditSavesNothing(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	_, err := svc.SetStay(context.Background(), uid, tid, 3, TripStay{CityName: "Porto", Name: "X"})
	require.ErrorIs(t, err, ErrInvalidEdit)
	require.Zero(t, mem.saves)
}

func TestService_OtherUsersTripIsNotFound(t *testing.T) {
	svc, _, _, tid := newServiceFixture()
	_, err := svc.ClearStay(context.Background(), uuid.New(), tid, 3, "Lisbon")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestService_AddFlightBuildsItsOwnLinks(t *testing.T) {
	svc, _, uid, tid := newServiceFixture()
	got, err := svc.AddFlight(context.Background(), uid, tid, 3, TripFlight{
		ID:     uuid.New(),
		Origin: flights.Place{Name: "New York", IATA: "JFK"}, Destination: flights.Place{Name: "Lisbon", IATA: "LIS"},
		DepartDate: day("2026-11-12"), Passengers: 2,
		Links: []flights.Link{{Provider: "evil", URL: "https://phish.example"}},
	})
	require.NoError(t, err)
	require.Len(t, got.Flights, 1)
	links := got.Flights[0].Links
	require.Len(t, links, 2)
	require.Equal(t, "google_flights", links[0].Provider)
	require.Equal(t, "skyscanner", links[1].Provider)
	for _, l := range links {
		require.NotContains(t, l.URL, "phish")
	}
}

func TestService_SetDatesThenRemoveFlightRoundTrip(t *testing.T) {
	svc, _, uid, tid := newServiceFixture()
	ctx := context.Background()
	tr, err := svc.SetDates(ctx, uid, tid, 3, day("2026-11-12"), day("2026-11-13"))
	require.NoError(t, err)
	tr, err = svc.AddFlight(ctx, uid, tid, tr.Version, TripFlight{
		Origin: flights.Place{Name: "Porto"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"),
	})
	require.NoError(t, err)
	tr, err = svc.RemoveFlight(ctx, uid, tid, tr.Version, tr.Flights[0].ID)
	require.NoError(t, err)
	require.Empty(t, tr.Flights)
	require.EqualValues(t, 6, tr.Version)
}

func TestService_FlightLinksRejectsWhatTheProtoWould(t *testing.T) {
	svc, _, _, _ := newServiceFixture()
	_, err := svc.FlightLinks(flights.Query{Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, Depart: day("2026-11-12"), Passengers: 40})
	require.ErrorIs(t, err, ErrInvalidEdit)
	_, err = svc.FlightLinks(flights.Query{Depart: day("2026-11-12"), Passengers: 1})
	require.ErrorIs(t, err, ErrInvalidEdit, "no places")
	links, err := svc.FlightLinks(flights.Query{Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, Depart: day("2026-11-12")})
	require.NoError(t, err)
	require.NotEmpty(t, links)
}

func TestService_ReplaceDaysRenumbersAndDatesFromStart(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	start, end := day("2026-11-12"), day("2026-11-17")
	mem.trip.StartDate, mem.trip.EndDate = &start, &end
	mem.trip.Stays = []TripStay{{CityName: "Lisbon", Name: "Hotel Avenida"}}
	oldID := uuid.New()
	got, err := svc.ReplaceDays(context.Background(), uid, tid, 3, []TripDay{
		{ID: oldID, DayNumber: 7, Stops: []TripStop{{ID: uuid.New(), Name: "Belém"}}},
		{DayNumber: 9},
		{DayNumber: 2},
	})
	require.NoError(t, err)
	require.Len(t, got.Days, 3)
	for i, d := range got.Days {
		require.EqualValues(t, i+1, d.DayNumber)
		require.Equal(t, start.AddDate(0, 0, i), *d.Date)
	}
	require.Equal(t, uuid.Nil, got.Days[0].ID, "a re-plan's days are new days")
	require.Equal(t, uuid.Nil, got.Days[0].Stops[0].ID)
	require.Len(t, got.Stays, 1, "stays survive a re-plan")
}

func TestService_ReplaceDaysRejects(t *testing.T) {
	svc, mem, uid, tid := newServiceFixture()
	_, err := svc.ReplaceDays(context.Background(), uid, tid, 2, []TripDay{{}})
	require.ErrorIs(t, err, ErrVersionConflict)
	_, err = svc.ReplaceDays(context.Background(), uid, tid, 3, nil)
	require.ErrorIs(t, err, ErrInvalidEdit)
	require.Zero(t, mem.saves)
}
