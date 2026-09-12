package service

import "testing"

func TestResolvePOITarget(t *testing.T) {
	cases := []struct {
		name string
		days int
		pro  bool
		want int
	}{
		// The floor holds for very short trips.
		{"one day free", 1, false, 8},
		{"two days free", 2, false, 12},
		{"four days free", 4, false, 24},

		// Five days is where the per-day rate drops, so the curve flattens
		// rather than jumping: 4d*6=24 then 5d*5=25.
		{"five days free", 5, false, 25},
		{"six days free", 6, false, 30},
		{"seven days free", 7, false, 35},

		// The ceiling.
		{"eight days free", 8, false, 40},
		{"ten days free", 10, false, 40},
		{"a month free", 30, false, 40},

		// Pro gets a higher ceiling but the same curve beneath it.
		{"ten days pro", 10, true, 50},
		{"a month pro", 30, true, 50},
		{"four days pro", 4, true, 24},

		// Defensive: a caller that never parsed a duration must still get a
		// usable answer rather than zero places.
		{"zero days", 0, false, 8},
		{"negative days", -3, false, 8},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolvePOITarget(c.days, c.pro); got != c.want {
				t.Errorf("resolvePOITarget(%d, pro=%v) = %d, want %d", c.days, c.pro, got, c.want)
			}
		})
	}
}

// The curve must never go backwards: a longer trip cannot ask for fewer places
// than a shorter one, even across the per-day rate change at five days.
func TestPOITargetIsMonotonic(t *testing.T) {
	for _, pro := range []bool{false, true} {
		prev := 0
		for days := 1; days <= 30; days++ {
			got := resolvePOITarget(days, pro)
			if got < prev {
				t.Fatalf("pro=%v: %d days asks for %d, fewer than %d days' %d", pro, days, got, days-1, prev)
			}
			prev = got
		}
	}
}

// Pro is never worse off than free, and the two only diverge at the ceiling.
func TestProIsNeverSmallerThanFree(t *testing.T) {
	for days := 1; days <= 30; days++ {
		free, pro := resolvePOITarget(days, false), resolvePOITarget(days, true)
		if pro < free {
			t.Fatalf("%d days: pro asks for %d, free for %d", days, pro, free)
		}
	}
}
