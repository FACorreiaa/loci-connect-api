package locitypes

import "testing"

func TestPriceTier(t *testing.T) {
	cases := map[string]int32{
		"$": 1, "$$": 2, "€€€": 3, "$$$$": 4, "$$$$$": 4, " £ £ ": 2,
		"$$-$$$": 2, "$$ – $$$": 2, "3": 3,
		"Moderate": 2, "inexpensive": 1, "Expensive": 3, "very expensive": 4, "Budget-friendly": 1,
		"": 0, "Free": 0, "€15-25": 0, "unknown": 0,
	}
	for in, want := range cases {
		if got := PriceTier(in); got != want {
			t.Errorf("PriceTier(%q) = %d want %d", in, got, want)
		}
	}
	if PriceTier("", "$$") != 2 {
		t.Error("falls through to later fields")
	}
}
