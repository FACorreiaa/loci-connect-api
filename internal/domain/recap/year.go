package recap

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Year in review rules. The recap of a year unlocks on 1 December, is built
// and pushed from then (after PushFromHour, in the traveller's zone), and the
// runner keeps picking up late arrivals until the end of January.
const (
	YearUnlockMonth = time.December
	YearUnlockDay   = 1
	// yearRunnerUntil is the last month (of the following year) in which the
	// runner still queues a year's recap on its own.
	yearRunnerUntil = time.January
)

// Eligibility: a year with less than this has too little to tell.
const (
	yearMinTrips  = 2
	yearMinCities = 3
	yearMinVisits = 10
)

// minPercentileActive is how many travellers must have been active in a year
// before a percentile means anything; maxPercentileShown is the worst rank
// worth a card ("top 80%" is not a compliment).
const (
	minPercentileActive = 200
	maxPercentileShown  = 50
)

// YearAccess decides when a traveller may open a year's recap. The zero
// value is the production rule: from 1 December of that year.
//
// For testing in production, UnlockOverride opens its own year on that date
// instead, and Allow, when not empty, limits the override to those
// travellers; everyone else still waits for 1 December.
type YearAccess struct {
	UnlockOverride time.Time
	Allow          map[uuid.UUID]bool
}

// UnlockDate is the local date userID's recap of year opens.
func (a YearAccess) UnlockDate(userID uuid.UUID, year int) time.Time {
	if !a.UnlockOverride.IsZero() && year == a.UnlockOverride.Year() && (len(a.Allow) == 0 || a.Allow[userID]) {
		return dateOnly(a.UnlockOverride)
	}
	return time.Date(year, YearUnlockMonth, YearUnlockDay, 0, 0, 0, 0, time.UTC)
}

// Unlocked reports whether userID may open year's recap on today (a local
// date). A past year always is; a year that has not started never is.
func (a YearAccess) Unlocked(userID uuid.UUID, year int, today time.Time) bool {
	switch {
	case year > today.Year():
		return false
	case year < today.Year():
		return true
	}
	return !today.Before(a.UnlockDate(userID, year))
}

// RunnerYear is the year whose recaps the runner works on at today (a UTC
// date), and whether it works on any: December of a year and January after
// it, or the override's year from its date on.
func (a YearAccess) RunnerYear(today time.Time) (int, bool) {
	if !a.UnlockOverride.IsZero() {
		y := a.UnlockOverride.Year()
		if today.Year() == y && !today.Before(dateOnly(a.UnlockOverride).AddDate(0, 0, -1)) {
			return y, true
		}
	}
	switch {
	case today.Month() == YearUnlockMonth:
		return today.Year(), true
	case today.Month() <= yearRunnerUntil:
		return today.Year() - 1, true
	}
	return 0, false
}

// YearCandidate is a traveller the runner may queue a year recap for.
type YearCandidate struct {
	UserID   uuid.UUID
	Timezone string
}

// YearFacts is everything a year in review is built from. Dates are the
// traveller's local dates, as midnight UTC.
type YearFacts struct {
	UserID   uuid.UUID
	Year     int
	Timezone string

	// Trips that started in the year, in date order.
	Trips []YearTrip
	// Cities the traveller was in during the year: on a dated trip day or
	// with a visit.
	Cities []YearCity
	// Places is every distinct place visited or marked done in the year.
	Places []Place

	// Taste is each trait's evidence before and during the year.
	Taste []TraitShift

	// Points earned in the year, the local dates any were earned, and badges.
	Points      int64
	ActiveDates []time.Time
	Badges      []string

	Contributions Contributions

	// Percentile is set when enough travellers were active to rank against.
	Percentile *Percentile

	Owner Owner
}

// YearTrip is one trip of the year.
type YearTrip struct {
	ID          uuid.UUID
	Start       time.Time
	Days        int
	CityName    string
	CountryCode string
}

// YearCity is one city of the year.
type YearCity struct {
	ID          *uuid.UUID
	Name        string
	Country     string
	CountryCode string
	Lat, Lon    float64
	HasCoords   bool
	// Days is the local dates spent there: trip days and visit days.
	Days   int
	Places int
	Trips  int
	// FirstCountryVisit is true when the year was the traveller's first in
	// the city's country.
	FirstCountryVisit bool
	ImageURL          string
}

// TraitShift is a taste trait's evidence weight before the year began and
// during it.
type TraitShift struct {
	Key, Label    string
	Before, Added float64
	// Evidence counts the year's evidence rows.
	Evidence int
}

// Contributions is what the traveller gave other travellers in the year.
type Contributions struct {
	Reviews, Lists, Posts, Submissions int
}

// Total is every contribution.
func (c Contributions) Total() int { return c.Reviews + c.Lists + c.Posts + c.Submissions }

// Percentile ranks the traveller's places against everyone active that year.
type Percentile struct {
	Places int
	// Rank is 1 for the most places; Active counts the travellers ranked.
	Rank, Active int
}

// TopPercent is "top N%": the smallest N with the traveller inside it.
func (p Percentile) TopPercent() int {
	if p.Active <= 0 || p.Rank <= 0 {
		return 0
	}
	n := (p.Rank*100 + p.Active - 1) / p.Active
	return max(n, 1)
}

// yearPeriod is the year's first and last date.
func yearPeriod(year int) (start, end time.Time) {
	return time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
}

// ParseYearAccess reads the testing overrides: a YYYY-MM-DD unlock date and
// user ids. Empty values give the production rule.
func ParseYearAccess(date string, ids []string) (YearAccess, error) {
	var a YearAccess
	if date = strings.TrimSpace(date); date != "" {
		d, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return YearAccess{}, fmt.Errorf("year unlock date %q: %w", date, err)
		}
		a.UnlockOverride = d
	}
	for _, s := range ids {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return YearAccess{}, fmt.Errorf("year allowlist id %q: %w", s, err)
		}
		if a.Allow == nil {
			a.Allow = map[uuid.UUID]bool{}
		}
		a.Allow[id] = true
	}
	return a, nil
}
