// Package tripspan works out how long a trip is from the traveller's own words.
//
// It exists because nothing in a ChatRequest carries a duration: "4 days in
// Madeira" and "a month in Madeira" reach the server as the same shape, and
// used to produce the same ten places. The answer here sizes the request.
//
// The package is deliberately pure — text in, days out, no database, no logger,
// no domain types — so it can be fuzzed, and so the legacy worker can use it as
// easily as the streaming path. Callers do the logging; see Span.Source.
//
// Dates are read day-first (3/5 is the third of May). Loci's cities are
// European and so are its travellers; this is the one genuinely ambiguous
// choice in the package and it is made once, here.
package tripspan

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Source says how a Span was decided. It is a label on a metric and a hint for
// later product copy: a rising SourceDefault share is a parser gap, not a shrug.
type Source string

const (
	SourceDateRange      Source = "date_range"
	SourceExplicitDays   Source = "explicit_days"
	SourceExplicitHours  Source = "explicit_hours"
	SourceExplicitWeeks  Source = "explicit_weeks"
	SourceExplicitMonths Source = "explicit_months"
	SourcePhrase         Source = "phrase"
	SourceDefault        Source = "default"
)

const (
	// DefaultDays is what an unparseable request is planned over. Refusing to
	// plan is a worse answer than planning a sample, and two days is a sample
	// rather than a guess at a whole trip.
	DefaultDays = 2
	// MaxDays caps the horizon. Past a month a single answer is a catalogue,
	// and the quality of a generated list falls off long before that.
	MaxDays = 30
)

// Span is a parsed planning horizon.
type Span struct {
	// Days is always in [1, MaxDays].
	Days int
	// Source is how Days was decided.
	Source Source
	// Match is the substring responsible, for logs. Empty when Source is
	// SourceDefault.
	Match string
	// Clamped reports that the request asked for longer than MaxDays. The
	// Source still says how it was read: "understood you and capped it" is not
	// the same failure as "did not understand you".
	Clamped bool
}

// Parsed reports whether the duration came from the text rather than from the
// default.
func (s Span) Parsed() bool { return s.Source != SourceDefault }

// Parse reads a planning horizon out of a traveller's request.
//
// Precedence is date range, then an explicit number with a unit, then a weak
// phrase, then the default. A range beats a duration because "5 days in
// Madeira, 3-9 May" means the dates: the number is the traveller's estimate and
// the dates are their booking.
//
// Parse never fails. The worst case is DefaultDays.
func Parse(text string) Span { return ParseAt(time.Now(), text) }

// ParseAt is Parse with an explicit notion of now, which bare date ranges need
// in order to pick a year. A test that depends on the wall clock is a test that
// fails in December.
func ParseAt(now time.Time, text string) Span {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return Span{Days: DefaultDays, Source: SourceDefault}
	}

	if s, ok := parseRange(now, t); ok {
		return s
	}
	if s, ok := parseExplicit(t); ok {
		return s
	}
	if s, ok := parsePhrase(t); ok {
		return s
	}
	return Span{Days: DefaultDays, Source: SourceDefault}
}

// clamp bounds days into [1, MaxDays], reporting whether it had to.
func clamp(days int) (int, bool) {
	if days < 1 {
		return 1, false
	}
	if days > MaxDays {
		return MaxDays, true
	}
	return days, false
}

// ---------------------------------------------------------------- explicit

// wordNumbers are the counts a traveller is likely to spell out. "a" and "an"
// are here so that "a month" is an explicit month rather than a phrase, which
// keeps the phrase table small enough to read.
var wordNumbers = map[string]int{
	"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11,
	"twelve": 12,
}

// number matches a digit run or a spelled-out count. A bare number with no unit
// is never a duration — "Room 101 in Madeira" is not a 101-day trip — which is
// why every pattern below requires a unit word.
const numberPattern = `(\d{1,4}|a|an|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)`

var (
	// Nights are counted as days, deliberately: a three-night stay still has
	// three days worth of places to fill, and erring long here is the cheaper
	// mistake.
	reDays   = regexp.MustCompile(numberPattern + `[\s-]*\b(?:days?|nights?)\b`)
	reHours  = regexp.MustCompile(numberPattern + `[\s-]*\b(?:hours?|hrs?|h)\b`)
	reWeeks  = regexp.MustCompile(numberPattern + `[\s-]*\bweeks?\b`)
	reMonths = regexp.MustCompile(numberPattern + `[\s-]*\bmonths?\b`)
)

func parseNumber(s string) (int, bool) {
	if n, ok := wordNumbers[s]; ok {
		return n, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// parseExplicit reads "4 days", "48 hours", "2 weeks", "a month".
//
// Hours are rounded up: 36 hours is a two-day trip, not a one-day trip with a
// long evening.
func parseExplicit(t string) (Span, bool) {
	type unit struct {
		re     *regexp.Regexp
		source Source
		toDays func(int) int
	}
	// Ordered smallest unit first so that "2 days" in a sentence that also says
	// "week" reads as the more specific of the two.
	for _, u := range []unit{
		{reHours, SourceExplicitHours, func(n int) int {
			return int(math.Max(1, math.Ceil(float64(n)/24)))
		}},
		{reDays, SourceExplicitDays, func(n int) int { return n }},
		{reWeeks, SourceExplicitWeeks, func(n int) int { return n * 7 }},
		{reMonths, SourceExplicitMonths, func(n int) int { return n * 30 }},
	} {
		m := u.re.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		n, ok := parseNumber(m[1])
		if !ok {
			continue
		}
		days, clamped := clamp(u.toDays(n))
		return Span{Days: days, Source: u.source, Match: strings.TrimSpace(m[0]), Clamped: clamped}, true
	}
	return Span{}, false
}

// ------------------------------------------------------------------ phrase

// phrases are the vague durations people actually type. Longest first, so that
// "long weekend" is not read as "weekend".
var phrases = []struct {
	text string
	days int
}{
	{"long weekend", 3},
	{"city break", 3},
	{"day trip", 1},
	{"daytrip", 1},
	{"fortnight", 14},
	{"overnight", 2},
	{"weekend", 2},
}

func parsePhrase(t string) (Span, bool) {
	for _, p := range phrases {
		if strings.Contains(t, p.text) {
			return Span{Days: p.days, Source: SourcePhrase, Match: p.text}, true
		}
	}
	return Span{}, false
}
