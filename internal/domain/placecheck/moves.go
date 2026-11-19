package placecheck

import (
	"sort"
	"time"
)

// maxMoves is how many moves one plan is offered at a time. A plan with more
// shut stops than this has a problem a list of cards will not fix.
const maxMoves = 3

// Move offers to take a stop whose place is shut all day to another day of
// the same plan on which it is open. Indexes are into the days given.
type Move struct {
	FromDay, Stop int
	ToDay         int
	// ToIndex is where the stop goes among the new day's stops, by its
	// start time; past the end when it has none.
	ToIndex int
	// On is the stop as it would be on the new day: open, with the hours
	// that make it so when the stop has a start time.
	On Check
}

// suggestMoves finds the moves. eval answers for a stop as if it were on
// another day.
//
// A move is only offered when it is known to work and to break nothing:
//
//   - the stop's place was found, its hours were read, and it does not open
//     at all on the stop's day
//   - on the new day it is open for the whole visit (or, for a stop with no
//     time, opens that day), by the same rule that called it shut
//   - neither day is fixed or already past, and the new day is still to
//     come: today's hours may already be gone by the time the card is read
//   - both days are in the same city, and the new day is not a public
//     holiday, when listed hours are least to be trusted
//   - the visit does not overlap a timed stop already on the new day
//
// A stop keeps its times when it moves, so no other stop's answer changes.
// The nearest day that works is offered; of two as near, the later one.
func suggestMoves(days []Day, checks [][]Check, eval func(day int, s Stop) Check, now time.Time) []Move {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	usable := func(d Day) bool {
		return d.Date != nil && !d.Fixed && !d.Date.Before(today)
	}

	// Visits already timed on each day, growing as moves are offered so two
	// moved stops are not put on top of each other.
	taken := make([][]span, len(days))
	for i, d := range days {
		for _, s := range d.Stops {
			if sp, ok := spanOf(s); ok {
				taken[i] = append(taken[i], sp)
			}
		}
	}

	var out []Move
	for i, d := range days {
		if !usable(d) || i >= len(checks) {
			continue
		}
		for j, s := range d.Stops {
			if len(out) == maxMoves {
				return out
			}
			if j >= len(checks[i]) || !shutAllDay(checks[i][j]) {
				continue
			}
			to, on, ok := openDay(days, i, s, eval, usable, today, taken)
			if !ok {
				continue
			}
			if sp, timed := spanOf(s); timed {
				taken[to] = append(taken[to], sp)
			}
			out = append(out, Move{FromDay: i, Stop: j, ToDay: to, ToIndex: indexFor(days[to], s), On: on})
		}
	}
	return out
}

// shutAllDay reports a stop the map is sure does not open on its day.
func shutAllDay(c Check) bool {
	return c.Status == StatusChecked && c.Hours == HoursShutThatDay && !c.HolidayHoursUnknown
}

// openDay is the nearest other day s could move to.
func openDay(days []Day, from int, s Stop, eval func(int, Stop) Check, usable func(Day) bool, today time.Time, taken [][]span) (int, Check, bool) {
	order := make([]int, 0, len(days))
	for k := range days {
		if k != from {
			order = append(order, k)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		da, db := abs(order[a]-from), abs(order[b]-from)
		if da != db {
			return da < db
		}
		return order[a] > order[b]
	})
	sp, timed := spanOf(s)
	for _, k := range order {
		d := days[k]
		if !usable(d) || !d.Date.After(today) || d.City != days[from].City {
			continue
		}
		on := eval(k, s)
		if on.Status != StatusChecked || on.Holiday != "" || on.HolidayHoursUnknown {
			continue
		}
		if timed && on.Hours != HoursOpen || !timed && on.Hours != HoursOpenThatDay {
			continue
		}
		if timed && overlaps(sp, taken[k]) {
			continue
		}
		return k, on, true
	}
	return 0, Check{}, false
}

// span is a visit's minutes of the day, end exclusive.
type span struct{ start, end int }

func spanOf(s Stop) (span, bool) {
	if s.StartMinute == nil {
		return span{}, false
	}
	start := int(*s.StartMinute)
	end := start + 1
	if s.DurationMinutes != nil && *s.DurationMinutes > 0 {
		end = start + int(*s.DurationMinutes)
	}
	return span{start, end}, true
}

func overlaps(sp span, others []span) bool {
	for _, o := range others {
		if sp.start < o.end && o.start < sp.end {
			return true
		}
	}
	return false
}

// indexFor is where s sits among d's stops by start time: after every timed
// stop that starts no later. A stop with no time goes last.
func indexFor(d Day, s Stop) int {
	if s.StartMinute == nil {
		return len(d.Stops)
	}
	at := 0
	for k, other := range d.Stops {
		if other.StartMinute != nil && *other.StartMinute <= *s.StartMinute {
			at = k + 1
		}
	}
	return at
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
