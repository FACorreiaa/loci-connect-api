package trip

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func editTrip() (*Trip, []uuid.UUID, []uuid.UUID) {
	days := []uuid.UUID{uuid.New(), uuid.New()}
	stops := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	return &Trip{Days: []TripDay{
		{ID: days[0], DayNumber: 1, Stops: []TripStop{{ID: stops[0], Name: "A"}, {ID: stops[1], Name: "B", OrderIndex: 1}}},
		{ID: days[1], DayNumber: 2, Stops: []TripStop{{ID: stops[2], Name: "C"}}},
	}}, days, stops
}

func names(d TripDay) []string {
	out := []string{}
	for i, s := range d.Stops {
		if int(s.OrderIndex) != i {
			return []string{"bad order index"}
		}
		out = append(out, s.Name)
	}
	return out
}

func TestStopEdits(t *testing.T) {
	tr, days, _ := editTrip()
	require.NoError(t, addStop(tr, days[1], TripStop{Name: "D"}))
	require.Equal(t, []string{"C", "D"}, names(tr.Days[1]))
	require.ErrorIs(t, addStop(tr, uuid.New(), TripStop{Name: "X"}), ErrDayNotFound)

	tr, _, stops := editTrip()
	got, err := removeStop(tr, stops[0])
	require.NoError(t, err)
	require.Equal(t, "A", got.Name)
	require.Equal(t, []string{"B"}, names(tr.Days[0]))
	_, err = removeStop(tr, stops[0])
	require.ErrorIs(t, err, ErrStopNotFound)
	require.ErrorIs(t, err, ErrInvalidEdit)

	tr, _, stops = editTrip()
	require.NoError(t, replaceStop(tr, stops[1], TripStop{Name: "B2"}))
	require.Equal(t, []string{"A", "B2"}, names(tr.Days[0]))

	tr, days, stops = editTrip()
	require.NoError(t, moveStop(tr, stops[0], days[1], 1))
	require.Equal(t, []string{"B"}, names(tr.Days[0]))
	require.Equal(t, []string{"C", "A"}, names(tr.Days[1]))
	require.Equal(t, stops[0], tr.Days[1].Stops[1].ID, "a moved stop keeps its id")

	tr, days, stops = editTrip()
	require.NoError(t, moveStop(tr, stops[1], days[0], 0), "within a day")
	require.Equal(t, []string{"B", "A"}, names(tr.Days[0]))
	require.NoError(t, moveStop(tr, stops[1], days[1], 99), "past the end appends")
	require.Equal(t, []string{"C", "B"}, names(tr.Days[1]))
	require.ErrorIs(t, moveStop(tr, stops[1], uuid.New(), 0), ErrDayNotFound)

	s, d := FindStop(tr, stops[2])
	require.Equal(t, "C", s.Name)
	require.Equal(t, int32(2), d.DayNumber)
	require.Nil(t, FindDay(tr, uuid.New()))
}
