package placecheck

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A plan of consecutive days from Monday 13 April 2026 (the service's clock
// reads Tuesday the 14th). Each day is in Lisbon unless a test says not.
func planFrom(first time.Time, stops ...[]Stop) []Day {
	days := make([]Day, len(stops))
	for i, s := range stops {
		date := first.AddDate(0, 0, i)
		days[i] = Day{Date: &date, Centre: lisbon, HasCentre: true, City: "lisbon", Stops: s}
	}
	return days
}

// holidayOn is a calendar with holidays on the dates it names.
type holidayOn map[string]string

func (h holidayOn) Holiday(_ context.Context, _, _ float64, day time.Time) string {
	return h[day.Format(time.DateOnly)]
}

func visit(id string, start, minutes int32) Stop {
	return Stop{POIID: id, Name: id, StartMinute: ptr(start), DurationMinutes: ptr(minutes)}
}

func held(hours map[string]string) *fakeStore {
	rows := map[string]Verification{}
	for id, h := range hours {
		v := *matched(h)
		v.POIID = id
		rows[id] = v
	}
	return &fakeStore{rows: rows}
}

var (
	wednesday = tuesday.AddDate(0, 0, 1)
	// The museum is shut on Wednesdays; everything else is open every day.
	museum = "Th-Tu 10:00-18:00; We off"
	always = "Mo-Su 09:00-22:00"
)

func TestMovesOffersTheNearestDayThePlaceIsOpen(t *testing.T) {
	store := held(map[string]string{"museum": museum, "cafe": always, "tower": always})
	days := planFrom(wednesday,
		[]Stop{visit("cafe", 540, 60), visit("museum", 660, 90)}, // Wed: shut
		[]Stop{visit("tower", 600, 30), visit("cafe", 900, 60)},  // Thu
		[]Stop{visit("tower", 600, 30)},                          // Fri
	)

	got := newPlanner(store, &fakeFinder{}, nil).Moves(context.Background(), days)

	require.Len(t, got, 1)
	m := got[0]
	require.Equal(t, 0, m.FromDay)
	require.Equal(t, 1, m.Stop)
	require.Equal(t, 1, m.ToDay, "Thursday is as near as it gets")
	require.Equal(t, 1, m.ToIndex, "after the 10:00 stop and before the 15:00 one")
	require.Equal(t, HoursOpen, m.On.Hours)
	require.Equal(t, 600, m.On.OpensMinute)
	require.Equal(t, 1080, m.On.ClosesMinute)
}

func TestMovesPrefersTheLaterOfTwoDaysAsNear(t *testing.T) {
	store := held(map[string]string{"museum": museum})
	days := planFrom(tuesday,
		nil,                              // Tue
		[]Stop{visit("museum", 660, 90)}, // Wed: shut
		nil,                              // Thu
	)
	got := newPlanner(store, &fakeFinder{}, nil).Moves(context.Background(), days)
	require.Len(t, got, 1)
	require.Equal(t, 2, got[0].ToDay)
}

// Every reason not to offer a move. Each case is a plan in which Wednesday's
// museum is shut and the only other day is, for one reason, no good.
func TestMovesOffersNothingItCannotStandBehind(t *testing.T) {
	shutWed := []Stop{visit("museum", 660, 90)}
	cases := []struct {
		name  string
		hours string
		days  func() []Day
		cal   Calendar
	}{
		{name: "the other day is shut too", hours: "Mo,Tu 10:00-18:00", days: func() []Day {
			return planFrom(wednesday, shutWed, nil)
		}},
		{name: "open the other day, but not at that hour", hours: "Th-Tu 14:00-18:00; We off", days: func() []Day {
			return planFrom(wednesday, shutWed, nil)
		}},
		{name: "the visit would run past closing", hours: "Th-Tu 10:00-12:00; We off", days: func() []Day {
			return planFrom(wednesday, shutWed, nil)
		}},
		{name: "another stop is already there at that hour", hours: museum, days: func() []Day {
			return planFrom(wednesday, shutWed, []Stop{visit("tower", 720, 60)})
		}},
		{name: "the other day is in another city", hours: museum, days: func() []Day {
			d := planFrom(wednesday, shutWed, nil)
			d[1].City = "porto"
			return d
		}},
		{name: "the other day is a travel day", hours: museum, days: func() []Day {
			d := planFrom(wednesday, shutWed, nil)
			d[1].Fixed = true
			return d
		}},
		{name: "the stop's own day is already done", hours: museum, days: func() []Day {
			d := planFrom(wednesday, shutWed, nil)
			d[0].Fixed = true
			return d
		}},
		{name: "the other day has passed", hours: "Mo 10:00-18:00", days: func() []Day {
			// Monday the 13th, with the clock on Tuesday.
			d := planFrom(tuesday.AddDate(0, 0, -1), nil, []Stop{visit("museum", 660, 90)})
			return d
		}},
		{name: "the other day is a public holiday", hours: museum, cal: holidayOn{"2026-04-16": "Feriado"}, days: func() []Day {
			return planFrom(wednesday, shutWed, nil)
		}},
		{name: "the plan has no dates", hours: museum, days: func() []Day {
			d := planFrom(wednesday, shutWed, nil)
			d[0].Date, d[1].Date = nil, nil
			return d
		}},
		{name: "the hours cannot be read", hours: "Mo-Su sunrise-sunset", days: func() []Day {
			return planFrom(wednesday, shutWed, nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := held(map[string]string{"museum": tc.hours, "tower": always})
			got := newPlanner(store, &fakeFinder{}, tc.cal).Moves(context.Background(), tc.days())
			require.Empty(t, got)
		})
	}
}

func TestMovesLeavesAloneWhatIsNotShutAllDay(t *testing.T) {
	store := held(map[string]string{"late": "Mo-Su 14:00-18:00", "open": always})
	store.rows["gone"] = Verification{POIID: "gone", Source: SourceOSM, CheckedAt: tuesday, ExpiresAt: tuesday.Add(time.Hour)}
	days := planFrom(wednesday,
		[]Stop{visit("late", 600, 60), visit("open", 720, 60), visit("gone", 900, 60), visit("never-looked-up", 1000, 30)},
		nil,
	)
	require.Empty(t, newPlanner(store, &fakeFinder{}, nil).Moves(context.Background(), days))
}

func TestMovesForAStopWithNoTimeGoesLastOnADayThePlaceOpens(t *testing.T) {
	store := held(map[string]string{"museum": museum, "tower": always})
	days := planFrom(wednesday,
		[]Stop{{POIID: "museum", Name: "museum"}},
		[]Stop{visit("tower", 600, 30), visit("tower", 900, 30)},
	)
	got := newPlanner(store, &fakeFinder{}, nil).Moves(context.Background(), days)
	require.Len(t, got, 1)
	require.Equal(t, 2, got[0].ToIndex)
	require.Equal(t, HoursOpenThatDay, got[0].On.Hours)
}

func TestMovesDoesNotPutTwoMovedStopsOnTopOfEachOther(t *testing.T) {
	store := held(map[string]string{"museum": museum, "gallery": museum})
	days := planFrom(wednesday,
		[]Stop{visit("museum", 660, 90), visit("gallery", 690, 60)}, // both shut, overlapping hours
		nil, // Thu
		nil, // Fri
	)
	got := newPlanner(store, &fakeFinder{}, nil).Moves(context.Background(), days)
	require.Len(t, got, 2)
	require.Equal(t, 1, got[0].ToDay)
	require.Equal(t, 2, got[1].ToDay, "Thursday at that hour is now taken")
}

func TestMovesStopsAtAFew(t *testing.T) {
	hours := map[string]string{}
	var shut []Stop
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		hours[id] = museum
		//nolint:gosec // a handful of fixture stops
		shut = append(shut, visit(id, int32(540+i*60), 30))
	}
	days := planFrom(wednesday, shut, nil)
	require.Len(t, newPlanner(held(hours), &fakeFinder{}, nil).Moves(context.Background(), days), maxMoves)
}

func TestMovesNeverAsksTheMap(t *testing.T) {
	finder := &fakeFinder{}
	p := newPlanner(&fakeStore{rows: map[string]Verification{}}, finder, nil)
	days := planFrom(wednesday, []Stop{visit("museum", 660, 90)}, nil)
	require.Empty(t, p.Moves(context.Background(), days))
	p.wg.Wait()
	require.Empty(t, finder.asked)
}
