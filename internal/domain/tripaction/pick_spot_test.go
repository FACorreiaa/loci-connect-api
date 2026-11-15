package tripaction

import "testing"

func TestPickSpot(t *testing.T) {
	cases := []struct {
		name  string
		query string
		names []string
		want  int
	}{
		{
			"the place named wins over a similar one ranked first", "Time Out Market",
			[]string{"Mercado de Campo de Ourique", "Time Out Market", "Time Out Market Lisboa"},
			1,
		},
		{"case and spacing do not matter", " time out market ", []string{"Food Hall", "Time Out Market"}, 1},
		{
			"a longer official name still matches", "Manteigaria",
			[]string{"Pastéis de Belém", "Manteigaria - Fábrica de Pastéis de Nata"},
			1,
		},
		{"a description takes the search's first result", "a coffee shop", []string{"Fábrica Coffee Roasters", "Copenhagen Coffee Lab"}, 0},
		{"nameless results are skipped", "anything", []string{"", "  ", "Real Place"}, 2},
		{"nothing usable", "anything", []string{"", " "}, -1},
		{"no results", "anything", nil, -1},
	}
	for _, c := range cases {
		if got := pickSpot(c.query, c.names); got != c.want {
			t.Errorf("%s: pickSpot(%q) = %d, want %d", c.name, c.query, got, c.want)
		}
	}
}
