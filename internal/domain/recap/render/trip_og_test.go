package render

import "testing"

func lisbonTrip() TripCard {
	return TripCard{
		Title: "Lisbon, slowly", City: "Lisbon", Days: 3, Stops: 14, Checked: 13, Answered: 14,
		Route: [][2]float64{
			{38.6916, -9.2160},
			{38.6979, -9.2068},
			{38.7077, -9.1365},
			{38.7139, -9.1335},
			{38.7163, -9.1313},
			{38.7251, -9.1137},
			{38.7372, -9.1540},
		},
	}
}

func TestTripOG(t *testing.T) {
	for name, card := range map[string]TripCard{
		"full":         lisbonTrip(),
		"empty":        {},
		"no route":     {Title: "Porto", Days: 2, Stops: 6, Checked: 6, Answered: 6},
		"one point":    {Title: "Porto", Route: [][2]float64{{41.15, -8.61}}},
		"unplaced":     {Title: "Porto", Route: [][2]float64{{0, 0}, {0, 0}, {41.15, -8.61}}},
		"long title":   {Title: "A very long name for a trip that somebody typed without thinking about link previews at all", City: "Santiago de Compostela", Days: 12, Stops: 96, Checked: 96, Answered: 96, Route: lisbonTrip().Route},
		"non-latin":    {Title: "東京の三日間", City: "東京", Days: 3, Stops: 9},
		"over-counted": {Title: "Odd", Checked: 9, Answered: 4},
	} {
		t.Run(name, func(t *testing.T) {
			b, err := TripOG(card)
			if err != nil {
				t.Fatal(err)
			}
			decode(t, b, OGW, OGH)
		})
	}
}

func TestTripFacts(t *testing.T) {
	for _, tc := range []struct {
		card TripCard
		want string
	}{
		{lisbonTrip(), "Lisbon · 3 days · 14 stops"},
		{TripCard{City: " Porto ", Days: 1, Stops: 1}, "Porto · 1 day · 1 stop"},
		{TripCard{Days: 2}, "2 days"},
		{TripCard{}, ""},
	} {
		if got := tripFacts(tc.card); got != tc.want {
			t.Errorf("tripFacts(%+v) = %q, want %q", tc.card, got, tc.want)
		}
	}
}

func TestTripStampMakesNoClaimItCannotBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		card TripCard
		want string
	}{
		{"every stop found", TripCard{Checked: 14, Answered: 14}, "14 of 14 stops checked"},
		{"some not found", TripCard{Checked: 11, Answered: 14}, "11 of 14 stops checked"},
		{"nothing answered yet", TripCard{Stops: 14}, ""},
		{"answered, none found", TripCard{Answered: 14}, ""},
		{"never more than were answered", TripCard{Checked: 9, Answered: 4}, "4 of 4 stops checked"},
	} {
		if got := tripStamp(tc.card); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
