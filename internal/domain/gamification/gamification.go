// Package gamification is Loci's points layer: a ledger of things a traveller
// actually did, the streak, level and badges it earns, and leaderboards shared
// only with friends.
//
// Points are awarded by the server, in the code paths that already record the
// action (a visit, a search, a scout report). Every award is keyed by the
// action instance, so a retried request can never count twice.
package gamification

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

// Kind is what earned the points. Values match PointsKind in
// gamification.proto and the points_events.kind column.
type Kind int16

const (
	KindDailyCheckIn     Kind = 1
	KindDailySearch      Kind = 2
	KindPlaceVisited     Kind = 3
	KindNewCity          Kind = 4
	KindScoutClaim       Kind = 5
	KindPlaceSubmission  Kind = 6
	KindTripDayCompleted Kind = 7
	KindTripCompleted    Kind = 8
)

// Rule is what one kind is worth and how often it can count per local day
// (0: no daily cap beyond the award's own key).
type Rule struct {
	Points   int
	DailyCap int
}

// Rules is the whole economy in one place, so it can be tuned without
// touching the paths that award.
var Rules = map[Kind]Rule{
	KindDailyCheckIn:     {Points: 5},
	KindDailySearch:      {Points: 5},
	KindPlaceVisited:     {Points: 10, DailyCap: 20},
	KindNewCity:          {Points: 50},
	KindScoutClaim:       {Points: 15},
	KindPlaceSubmission:  {Points: 25},
	KindTripDayCompleted: {Points: 20},
	KindTripCompleted:    {Points: 100},
}

var (
	// ErrNotFound is a trip or day the caller does not own.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is input the service refuses (an unknown timezone, a day
	// with no stops).
	ErrInvalid = errors.New("invalid request")
)

// Award is one action to score. RefKey names the action instance; the same
// (user, kind, ref key) is only ever counted once.
type Award struct {
	UserID uuid.UUID
	Kind   Kind
	RefKey string
	Label  string
}

// Event is a ledger row.
type Event struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Kind      Kind
	RefKey    string
	Points    int
	Label     string
	LocalDate time.Time
	CreatedAt time.Time
}

// Totals is a user's running progress.
type Totals struct {
	TotalPoints    int64
	CurrentStreak  int
	LongestStreak  int
	LastActiveDate *time.Time
	Timezone       string
}

// LevelFor turns a total into a level and the points still needed for the
// next one. Level L starts at 50·L·(L−1) points: 0, 100, 300, 600, 1000…, so
// each level asks a little more than the one before.
func LevelFor(total int64) (level int, toNext int64) {
	level = 1
	for threshold(level+1) <= total {
		level++
	}
	return level, threshold(level+1) - total
}

func threshold(level int) int64 { return 50 * int64(level) * int64(level-1) }

// NextStreak is the streak after a check-in on today, given the last active
// day: yesterday extends it, today keeps it, anything older starts over.
func NextStreak(current int, last *time.Time, today time.Time) int {
	if last == nil {
		return 1
	}
	switch daysBetween(*last, today) {
	case 0:
		if current < 1 {
			return 1
		}
		return current
	case 1:
		return current + 1
	default:
		return 1
	}
}

func daysBetween(a, b time.Time) int {
	ad := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	bd := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(bd.Sub(ad).Hours() / 24)
}

// LocalDate is the calendar day it is at now in tz, as a UTC midnight. An
// unknown zone falls back to UTC.
func LocalDate(now time.Time, tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	t := now.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ValidTimezone reports whether tz is an IANA zone the server knows.
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// LocalHour is the hour of day it is at now in tz.
func LocalHour(now time.Time, tz string) int {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	return now.In(loc).Hour()
}

// Period is a leaderboard window, in local dates, inclusive. A zero From
// means all time.
type Period struct {
	From, To time.Time
}

// PeriodKind mirrors LeaderboardPeriod.
type PeriodKind int

const (
	PeriodWeek PeriodKind = iota + 1
	PeriodMonth
	PeriodAllTime
)

// PeriodFor is the window containing today: Monday to Sunday, the calendar
// month, or everything.
func PeriodFor(kind PeriodKind, today time.Time) Period {
	switch kind {
	case PeriodWeek:
		offset := (int(today.Weekday()) + 6) % 7 // Monday is 0
		from := today.AddDate(0, 0, -offset)
		return Period{From: from, To: from.AddDate(0, 0, 6)}
	case PeriodMonth:
		from := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		return Period{From: from, To: from.AddDate(0, 1, -1)}
	default:
		return Period{}
	}
}

// Metric mirrors LeaderboardMetric.
type Metric int

const (
	MetricPoints Metric = iota + 1
	MetricCities
	MetricPlaces
)

// Badge is one achievement.
type Badge struct {
	ID          string
	Title       string
	Description string
	// Earned reports whether the counts reach it.
	Earned func(c Counts) bool
}

// Counts are what badges are judged on.
type Counts struct {
	Cities        int
	Places        int
	ScoutClaims   int
	TripsDone     int
	LongestStreak int
}

// Badges is every badge, in the order the app shows them.
var Badges = []Badge{
	{ID: "first-city", Title: "First city", Description: "Visit your first city with Loci.", Earned: func(c Counts) bool { return c.Cities >= 1 }},
	{ID: "globetrotter", Title: "Globetrotter", Description: "Visit 10 cities.", Earned: func(c Counts) bool { return c.Cities >= 10 }},
	{ID: "trailblazer", Title: "Trailblazer", Description: "Visit 50 places on the spot.", Earned: func(c Counts) bool { return c.Places >= 50 }},
	{ID: "streak-7", Title: "Week streak", Description: "Open Loci 7 days in a row.", Earned: func(c Counts) bool { return c.LongestStreak >= 7 }},
	{ID: "streak-30", Title: "Month streak", Description: "Open Loci 30 days in a row.", Earned: func(c Counts) bool { return c.LongestStreak >= 30 }},
	{ID: "trip-finisher", Title: "Trip finisher", Description: "Walk every day of a trip.", Earned: func(c Counts) bool { return c.TripsDone >= 1 }},
	{ID: "local-scout", Title: "Local scout", Description: "Have 10 field reports confirmed.", Earned: func(c Counts) bool { return c.ScoutClaims >= 10 }},
}

// BadgeByID finds a badge.
func BadgeByID(id string) (Badge, bool) {
	for _, b := range Badges {
		if b.ID == id {
			return b, true
		}
	}
	return Badge{}, false
}

// Visit verification: a place counts when the device was this close to it,
// this recently, with a fix at least this good. A city counts when the device
// was within MaxCityDistanceKm of where the visit was recorded.
const (
	MaxVisitDistanceM = 150.0
	MaxVisitFixAge    = 15 * time.Minute
	MaxVisitAccuracyM = 100.0
	MaxCityDistanceKm = 50.0
)

// Fix is where the device said it was.
type Fix struct {
	Latitude, Longitude float64
	AccuracyM           float64
	ObservedAt          time.Time
}

// Fresh reports whether the fix is recent and precise enough to score.
func (f Fix) Fresh(now time.Time) bool {
	age := now.Sub(f.ObservedAt)
	return !f.ObservedAt.IsZero() && age >= -time.Minute && age <= MaxVisitFixAge && f.AccuracyM <= MaxVisitAccuracyM
}

// DistanceM is the great-circle distance in metres.
func DistanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	toRad := math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
