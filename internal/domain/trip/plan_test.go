package trip

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func threeDayLisbon() *Trip {
	return &Trip{CityName: "Lisbon", Days: []TripDay{{DayNumber: 1}, {DayNumber: 2}, {DayNumber: 3}}}
}

func TestApplyDates_StampsDaysAndNeverResizes(t *testing.T) {
	tr := threeDayLisbon()
	require.NoError(t, applyDates(tr, day("2026-11-12"), day("2026-11-16")))
	require.Equal(t, "2026-11-12", tr.StartDate.Format(time.DateOnly))
	require.Equal(t, "2026-11-16", tr.EndDate.Format(time.DateOnly))
	require.Len(t, tr.Days, 3, "dates never add or drop days")
	require.Equal(t, "2026-11-12", tr.Days[0].Date.Format(time.DateOnly))
	require.Equal(t, "2026-11-14", tr.Days[2].Date.Format(time.DateOnly))
}

func TestApplyDates_Rejects(t *testing.T) {
	require.ErrorIs(t, applyDates(threeDayLisbon(), day("2026-11-16"), day("2026-11-12")), ErrInvalidEdit)
	// 2026-11-01 .. 2026-12-01 is 31 days inclusive.
	require.ErrorIs(t, applyDates(threeDayLisbon(), day("2026-11-01"), day("2026-12-01")), ErrInvalidEdit)
	require.NoError(t, applyDates(threeDayLisbon(), day("2026-11-01"), day("2026-11-30")), "30 days is allowed")
}

func TestUpsertStay_MatchesCityLooselyAndReplaces(t *testing.T) {
	tr := threeDayLisbon()
	require.NoError(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "Hotel Avenida", StarRating: "4"}))
	require.NoError(t, upsertStay(tr, TripStay{CityName: "  lisbon ", Name: "Pestana Palace", StarRating: "5"}))
	require.Len(t, tr.Stays, 1, "same city, different spelling, replaces")
	require.Equal(t, "Pestana Palace", tr.Stays[0].Name)
	require.Equal(t, "lisbon", tr.Stays[0].CityName, "stored trimmed")
}

func TestUpsertStay_Rejects(t *testing.T) {
	tr := threeDayLisbon()
	require.ErrorIs(t, upsertStay(tr, TripStay{CityName: "Porto", Name: "X"}), ErrInvalidEdit, "not a city on the trip")
	require.ErrorIs(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "  "}), ErrInvalidEdit, "needs a name")
	in, out := day("2026-11-14"), day("2026-11-12")
	require.ErrorIs(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "X", CheckIn: &in, CheckOut: &out}), ErrInvalidEdit)
}

func TestHasCity_MultiCity(t *testing.T) {
	tr := &Trip{CityName: "Lisbon", Cities: []TripCity{{CityName: "Lisbon"}, {CityName: "Porto"}}}
	require.True(t, tr.HasCity("PORTO"))
	require.False(t, tr.HasCity("Faro"))
}

func TestRemoveStay(t *testing.T) {
	tr := threeDayLisbon()
	require.NoError(t, upsertStay(tr, TripStay{CityName: "Lisbon", Name: "Hotel Avenida"}))
	require.NoError(t, removeStay(tr, "LISBON"))
	require.Empty(t, tr.Stays)
	require.ErrorIs(t, removeStay(tr, "Lisbon"), ErrInvalidEdit, "nothing to remove")
}

func TestAppendAndRemoveFlight(t *testing.T) {
	tr := threeDayLisbon()
	clientID := uuid.New()
	f, err := appendFlight(tr, TripFlight{
		ID:     clientID,
		Origin: flights.Place{Name: "New York", IATA: "JFK"}, Destination: flights.Place{Name: "Lisbon", IATA: "LIS"},
		DepartDate: day("2026-11-12"),
	})
	require.NoError(t, err)
	require.NotEqual(t, clientID, f.ID, "the server assigns flight ids")
	require.EqualValues(t, 1, f.Passengers, "defaults to one traveller")
	require.Len(t, tr.Flights, 1)

	require.ErrorIs(t, removeFlight(tr, uuid.New()), ErrInvalidEdit)
	require.NoError(t, removeFlight(tr, f.ID))
	require.Empty(t, tr.Flights)
}

func TestAppendFlight_Rejects(t *testing.T) {
	back := day("2026-11-10")
	cases := map[string]TripFlight{
		"no origin":      {Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12")},
		"no destination": {Origin: flights.Place{Name: "NYC"}, DepartDate: day("2026-11-12")},
		"no depart date": {Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}},
		"return before":  {Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"), ReturnDate: &back},
		"bad iata":       {Origin: flights.Place{Name: "NYC", IATA: "jfk"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12")},
		"ten passengers": {Origin: flights.Place{Name: "NYC"}, Destination: flights.Place{Name: "Lisbon"}, DepartDate: day("2026-11-12"), Passengers: 10},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := appendFlight(threeDayLisbon(), f)
			require.ErrorIs(t, err, ErrInvalidEdit)
		})
	}
}
