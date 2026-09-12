package service

import (
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/tripspan"
)

// The whole point of the change, in one table: what a traveller types, and how
// many places they get. Parsing and sizing are each tested on their own; this
// pins the two together, because it is the pair that anybody would check by
// hand and the pair a regression would be noticed in.
//
// Before this work every row below returned roughly ten.
func TestWhatATravellerTypesDecidesHowManyPlaces(t *testing.T) {
	at := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		request string
		pro     bool
		days    int
		source  tripspan.Source
		places  int
	}{
		{"4 days in Madeira", false, 4, tripspan.SourceExplicitDays, 24},
		{"a weekend in Madeira", false, 2, tripspan.SourcePhrase, 12},
		{"a week in Madeira", false, 7, tripspan.SourceExplicitWeeks, 35},

		// A month is a catalogue, not one answer: it stops at the ceiling, and
		// the ceiling is the only thing the plan changes.
		{"a month in Madeira", false, 30, tripspan.SourceExplicitMonths, 40},
		{"a month in Madeira", true, 30, tripspan.SourceExplicitMonths, 50},

		// The dates are the booking; the number of days is the guess.
		{"5 days in Madeira, 3-9 May", false, 7, tripspan.SourceDateRange, 35},

		// No duration stated: a two-day sample, never a refusal.
		{"things to do in Madeira", false, 2, tripspan.SourceDefault, 12},
		{"Madeira", false, 2, tripspan.SourceDefault, 12},
	}

	for _, c := range cases {
		t.Run(c.request, func(t *testing.T) {
			span := tripspan.ParseAt(at, c.request)
			if span.Days != c.days || span.Source != c.source {
				t.Fatalf("parsed %d days from %q, want %d from %q",
					span.Days, span.Source, c.days, c.source)
			}
			if got := resolvePOITarget(span.Days, c.pro); got != c.places {
				t.Errorf("pro=%v: asks for %d places, want %d", c.pro, got, c.places)
			}
		})
	}
}

// Whatever the request, the answer is sized within the bounds the rest of the
// system is built for: the packet can ground it, and the output budget can
// hold it.
func TestEveryRequestIsSizedWithinBounds(t *testing.T) {
	for _, request := range []string{
		"4 days in Madeira", "a month in Madeira", "", "Madeira",
		"1000 days in Madeira", "48 hours in Lisbon", "Room 101 in Madeira",
	} {
		for _, pro := range []bool{false, true} {
			target := resolvePOITarget(tripspan.Parse(request).Days, pro)

			if target < poiTargetMin || target > poiTargetMaxPro {
				t.Errorf("%q pro=%v: target %d outside [%d,%d]",
					request, pro, target, poiTargetMin, poiTargetMaxPro)
			}
			if evidenceLimitFor(target) < target {
				t.Errorf("%q pro=%v: %d places grounded in only %d candidates",
					request, pro, target, evidenceLimitFor(target))
			}
			if budget := outputTokenBudget(target); int(budget) < target*tokensPerPlace {
				t.Errorf("%q pro=%v: %d places share an output budget of %d tokens",
					request, pro, target, budget)
			}
		}
	}
}
