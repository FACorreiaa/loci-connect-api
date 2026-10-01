package tripaction

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var lisbon = []string{"Lisbon"}

func TestNormalize_SetDates(t *testing.T) {
	_, err := normalize(Action{Kind: KindSetDates, StartDate: "2026-11-12", EndDate: "2026-11-17"}, lisbon)
	require.NoError(t, err)
	for _, a := range []Action{
		{Kind: KindSetDates, StartDate: "12 Nov", EndDate: "2026-11-17"},
		{Kind: KindSetDates, StartDate: "2026-11-17", EndDate: "2026-11-12"},
		{Kind: KindSetDates, StartDate: "2026-11-01", EndDate: "2026-12-01"},
	} {
		_, err := normalize(a, lisbon)
		require.Error(t, err, a)
	}
}

func TestNormalize_SearchHotels(t *testing.T) {
	a, err := normalize(Action{Kind: KindSearchHotels, City: " lisbon", MinStars: 4}, lisbon)
	require.NoError(t, err)
	require.Equal(t, "Lisbon", a.City, "the trip's spelling")
	require.Equal(t, 5, a.MaxStars, "4 and up when no upper bound")

	a, err = normalize(Action{Kind: KindSearchHotels}, lisbon)
	require.NoError(t, err)
	require.Equal(t, "Lisbon", a.City, "a one-city trip fills the city in")
	require.Equal(t, 0, a.MinStars)

	_, err = normalize(Action{Kind: KindSearchHotels, City: "Porto", MinStars: 4}, lisbon)
	require.Error(t, err, "not on the trip")
	_, err = normalize(Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 5, MaxStars: 3}, lisbon)
	require.Error(t, err)
	_, err = normalize(Action{Kind: KindSearchHotels, City: "Lisbon", MinStars: 7}, lisbon)
	require.Error(t, err)
}

func TestNormalize_RegenerateDays(t *testing.T) {
	_, err := normalize(Action{Kind: KindRegenerateDays, Days: 6}, lisbon)
	require.NoError(t, err)
	for _, n := range []int{0, 31, -1} {
		_, err := normalize(Action{Kind: KindRegenerateDays, Days: n}, lisbon)
		require.Error(t, err, n)
	}
}

func TestNormalize_SearchFlights(t *testing.T) {
	a, err := normalize(Action{
		Kind: KindSearchFlights, Origin: Place{Name: "New York", IATA: "jfk"}, Destination: Place{Name: "Lisbon", IATA: "LIS"},
		Depart: "2026-11-12", Cabin: "first-ish",
	}, lisbon)
	require.NoError(t, err)
	require.Empty(t, a.Origin.IATA, "a malformed code is dropped, not fatal")
	require.Equal(t, "LIS", a.Destination.IATA)
	require.Equal(t, 1, a.Passengers)
	require.Empty(t, a.Cabin, "an unknown cabin is left unspecified")

	for _, bad := range []Action{
		{Kind: KindSearchFlights, Destination: Place{Name: "Lisbon"}, Depart: "2026-11-12"},
		{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon"}, Depart: "soon"},
		{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon"}, Depart: "2026-11-12", Return: "2026-11-01"},
		{Kind: KindSearchFlights, Origin: Place{Name: "NYC"}, Destination: Place{Name: "Lisbon"}, Depart: "2026-11-12", Passengers: 12},
	} {
		_, err := normalize(bad, lisbon)
		require.Error(t, err, bad)
	}
}

func TestNormalize_UnknownKind(t *testing.T) {
	_, err := normalize(Action{Kind: "book_restaurant"}, lisbon)
	require.Error(t, err)
}

func TestStars(t *testing.T) {
	for in, want := range map[string]float64{"4": 4, "4.5": 4.5, "4 stars": 4, "★★★★": 4, " 3 ": 3, "4★": 4, "4.5 ★": 4.5} {
		got, ok := starsOf(in)
		require.True(t, ok, in)
		require.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "luxury", "9", "0"} {
		_, ok := starsOf(in)
		require.False(t, ok, in)
	}
	require.True(t, starsWithin("4.5", 4, 4), "4.5 counts as a four-star")
	require.False(t, starsWithin("3", 4, 5))
	require.True(t, starsWithin("", 0, 5), "no filter keeps unrated hotels")
	require.False(t, starsWithin("", 4, 5), "a filter drops unrated hotels")
}
