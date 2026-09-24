package service

import "testing"

func TestParseTripCities(t *testing.T) {
	cases := []struct {
		name, raw   string
		wantCities  []string
		wantDays    []int
		wantOrdered bool
		wantMsg     string
	}{
		{
			"ordered with days",
			`{"cities":[{"city":"Lisbon","days":3},{"city":"Porto","days":2}],"ordered":true,"message":"itinerary"}`,
			[]string{"Lisbon", "Porto"},
			[]int{3, 2},
			true, "itinerary",
		},
		{
			"unordered",
			"```json\n{\"cities\":[{\"city\":\"Lisbon\"},{\"city\":\"Porto\"},{\"city\":\"Seville\"}],\"ordered\":false,\"message\":\"a week trip\"}\n```",
			[]string{"Lisbon", "Porto", "Seville"},
			[]int{0, 0, 0},
			false, "a week trip",
		},
		{
			"legacy single-city shape",
			`{"city":"Barcelona","message":"Find restaurants"}`,
			[]string{"Barcelona"},
			[]int{0},
			false, "Find restaurants",
		},
		{
			"duplicates and blanks collapse",
			`{"cities":[{"city":"Lisbon","days":2},{"city":" lisbon "},{"city":""},{"city":"Porto"}],"ordered":true,"message":"trip"}`,
			[]string{"Lisbon", "Porto"},
			[]int{2, 0},
			true, "trip",
		},
		{
			"no city keeps the original message",
			`{"cities":[],"ordered":false,"message":""}`,
			nil, nil, false, "ORIGINAL",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseTripCities(c.raw, "ORIGINAL")
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Cities) != len(c.wantCities) {
				t.Fatalf("cities = %+v, want %v", got.Cities, c.wantCities)
			}
			for i := range c.wantCities {
				if got.Cities[i].Name != c.wantCities[i] || got.Cities[i].Days != c.wantDays[i] {
					t.Errorf("city %d = %+v, want %s/%d", i, got.Cities[i], c.wantCities[i], c.wantDays[i])
				}
			}
			if got.Ordered != c.wantOrdered || got.Message != c.wantMsg {
				t.Errorf("ordered/message = %v/%q, want %v/%q", got.Ordered, got.Message, c.wantOrdered, c.wantMsg)
			}
		})
	}
}

func TestParseTripCities_Garbage(t *testing.T) {
	if _, err := parseTripCities("not json", "x"); err == nil {
		t.Fatal("expected an error for unparseable output")
	}
}
