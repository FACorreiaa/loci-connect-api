// Package gamification is Loci's field score: a ledger of exploration a
// traveller can show they did, counted per city and per ISO week, the
// permanent rank it earns, and weekly boards scoped to a city or to friends.
//
// Points are awarded by the server, in the code paths that already record the
// action (a save, a walked stop, a visit, a scout report). Every award is keyed
// by the action instance, so a retried request can never count twice. Opening
// the app and generating recommendations earn nothing.
package gamification

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
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
	KindPlaceSaved       Kind = 9
	KindPlaceKept        Kind = 10
	KindStopDone         Kind = 11
	KindNeighborhood     Kind = 12
	KindPlaceNote        Kind = 13
)

// Rule is what one kind is worth and how often it can count per local day
// (0: no daily cap beyond the award's own key). A retired kind awards
// nothing; its old rows stay in the ledger and count 0.
type Rule struct {
	Points   int
	DailyCap int
	Retired  bool
}

// Rules is the whole economy in one place, so it can be tuned without
// touching the paths that award. Points is what a new row counts toward the
// field score.
var Rules = map[Kind]Rule{
	KindDailyCheckIn:     {Retired: true},
	KindDailySearch:      {Retired: true},
	KindPlaceVisited:     {Points: 10, DailyCap: 10},
	KindNewCity:          {Points: 20},
	KindScoutClaim:       {Points: 15},
	KindPlaceSubmission:  {Points: 25},
	KindTripDayCompleted: {Points: 15},
	KindTripCompleted:    {Points: 25},
	KindPlaceSaved:       {Points: 2, DailyCap: 10},
	KindPlaceKept:        {Points: 5},
	KindStopDone:         {Points: 8},
	KindNeighborhood:     {Points: 10},
	KindPlaceNote:        {Points: 6},
}

// KeepAfter is how long a saved place has to stay saved to count as kept.
const KeepAfter = 7 * 24 * time.Hour

var (
	// ErrNotFound is a trip or day the caller does not own.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is input the service refuses (an unknown timezone, a day
	// with no stops).
	ErrInvalid = errors.New("invalid request")
)

// Award is one action to score. RefKey names the action instance; the same
// (user, kind, ref key) is only ever counted once. CityID is the city it
// happened in, when known: the award then also counts on that city's board.
type Award struct {
	UserID uuid.UUID
	Kind   Kind
	RefKey string
	Label  string
	CityID *uuid.UUID
}

// Event is a ledger row. Points is what it was awarded with; FieldPoints is
// what it counts toward the field score (the same for every row written since
// the field score, 0 for retired kinds).
type Event struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Kind        Kind
	RefKey      string
	Points      int
	FieldPoints int
	Label       string
	LocalDate   time.Time
	SeasonID    int
	CityID      *uuid.UUID
	CityName    string
	CreatedAt   time.Time
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

// SeasonID is the ISO week containing a local date, as year*100+week:
// 2026-10-08 is 202641. Seasons are written on every ledger row, so a board
// never re-derives one differently.
func SeasonID(localDate time.Time) int {
	y, w := localDate.ISOWeek()
	return y*100 + w
}

// SeasonStart is the Monday a season begins on, as a UTC midnight.
func SeasonStart(id int) time.Time {
	year, week := id/100, id%100
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	monday := jan4.AddDate(0, 0, -((int(jan4.Weekday()) + 6) % 7))
	return monday.AddDate(0, 0, (week-1)*7)
}

// SeasonShift is the season n weeks after id (n < 0: before).
func SeasonShift(id, n int) int {
	return SeasonID(SeasonStart(id).AddDate(0, 0, 7*n))
}

// SeasonCloseGrace is how long after a season's UTC end it closes: the last
// time zones (UTC-12) are still in Sunday for twelve hours after it.
const SeasonCloseGrace = 14 * time.Hour

// SeasonOver reports whether every time zone has finished the season.
func SeasonOver(id int, now time.Time) bool {
	return now.After(SeasonStart(id).AddDate(0, 0, 7).Add(SeasonCloseGrace))
}

// FieldRank is a permanent field rank. Values match FieldRank in the proto.
type FieldRank int

const (
	RankScout FieldRank = iota + 1
	RankWalker
	RankGuide
	RankLocal
	RankKeeper
)

// RankNames are the ranks in order, as the app writes them.
var RankNames = map[FieldRank]string{
	RankScout: "Scout", RankWalker: "Walker", RankGuide: "Guide", RankLocal: "Local", RankKeeper: "Keeper",
}

// Thresholds is the lifetime score each rank starts at, Scout first. Scout
// always starts at 0.
type Thresholds [5]int64

// Default rank thresholds. A city asks less than the whole field.
var (
	DefaultOverallThresholds = Thresholds{0, 60, 200, 500, 1200}
	DefaultCityThresholds    = Thresholds{0, 30, 100, 250, 600}
)

// ParseThresholds reads "0,60,200,500,1200". Empty gives def.
func ParseThresholds(s string, def Thresholds) (Thresholds, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != len(def) {
		return def, fmt.Errorf("rank thresholds: want %d values, got %d", len(def), len(parts))
	}
	var t Thresholds
	for i, p := range parts {
		v, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || v < 0 || (i > 0 && v <= t[i-1]) || (i == 0 && v != 0) {
			return def, fmt.Errorf("rank thresholds: %q must rise from 0", s)
		}
		t[i] = v
	}
	return t, nil
}

// RankFor is the rank a lifetime score holds and the score that reaches the
// next one (0 at the top rank).
func RankFor(score int64, t Thresholds) (FieldRank, int64) {
	r := RankScout
	for i := len(t) - 1; i >= 0; i-- {
		if score >= t[i] {
			r = FieldRank(i + 1)
			break
		}
	}
	if int(r) < len(t) {
		return r, t[r]
	}
	return r, 0
}

// MinNoteRunes is how long a note has to be to count.
const MinNoteRunes = 40

// NoteIsOriginal reports whether a note counts toward the field score: at
// least MinNoteRunes long, and not lifted from what Loci already wrote about
// the place (sources: its description, the AI's notes). A note sharing a run
// of 30 or more characters with a source, or half its five-word phrases, is
// taken as pasted. A heuristic, not proof; it only decides 6 points.
func NoteIsOriginal(note string, sources []string) bool {
	if utf8.RuneCountInString(strings.TrimSpace(note)) < MinNoteRunes {
		return false
	}
	n := normalizeText(note)
	shingles := wordShingles(n, 5)
	for _, src := range sources {
		src = normalizeText(src)
		if src == "" {
			continue
		}
		if sharesRun(n, src, 30) {
			return false
		}
		if len(shingles) > 0 {
			other := wordShingles(src, 5)
			shared := 0
			for sh := range shingles {
				if other[sh] {
					shared++
				}
			}
			if shared*2 >= len(shingles) {
				return false
			}
		}
	}
	return true
}

var foldAccents = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// normalizeText lowercases, drops accents and punctuation, and collapses
// spaces, so "Café, au lait!" and "cafe au lait" compare equal.
func normalizeText(s string) string {
	folded, _, err := transform.String(foldAccents, s)
	if err != nil {
		folded = s
	}
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(folded) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Slug is a name folded for keys: "São Bento" and "sao bento" match.
func Slug(name string) string {
	return strings.ReplaceAll(normalizeText(name), " ", "-")
}

func wordShingles(s string, n int) map[string]bool {
	words := strings.Fields(s)
	out := map[string]bool{}
	for i := 0; i+n <= len(words); i++ {
		out[strings.Join(words[i:i+n], " ")] = true
	}
	return out
}

func sharesRun(a, b string, n int) bool {
	ar := []rune(a)
	for i := 0; i+n <= len(ar); i++ {
		if strings.Contains(b, string(ar[i:i+n])) {
			return true
		}
	}
	return false
}

// Metric mirrors LeaderboardMetric.
type Metric int

const (
	MetricPoints Metric = iota + 1
	MetricCities
	MetricPlaces
)

// FieldMetric mirrors FieldBoardMetric.
type FieldMetric int

const (
	FieldOverall FieldMetric = iota + 1
	FieldPlacesKept
	FieldDaysFinished
)

// Scope mirrors FieldBoardScope.
type Scope int

const (
	ScopeCityWeek Scope = iota + 1
	ScopeFriendsWeek
	ScopePersonal
)

// StopStatus mirrors TripStopStatus.
type StopStatus int16

const (
	StopOpen    StopStatus = 1
	StopDone    StopStatus = 2
	StopSkipped StopStatus = 3
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
