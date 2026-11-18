// Package openinghours reads a deliberate subset of the OpenStreetMap
// opening_hours grammar and answers one question: is a place open for a visit
// at a given time on a given weekday.
//
// The subset is weekday lists and ranges, one or more time spans (including
// spans that run past midnight), "off"/"closed", "24/7", public holidays
// ("PH"), ";" rules that override earlier ones and "," rules that add to
// them. Everything else — months, dates, week numbers, sunrise/sunset,
// school holidays, comments, fallback rules, open-ended times — makes Parse
// return ErrUnsupported. That is the point: a caller shows "hours not
// checked" for those, never a guess.
package openinghours

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrUnsupported is returned by Parse for empty input and for any
// opening_hours value that uses grammar outside the subset.
var ErrUnsupported = errors.New("openinghours: unsupported opening_hours value")

const dayMinutes = 24 * 60

// State is the outcome of checking one visit against a Schedule.
type State int

const (
	// Unknown means the schedule could not answer (it was never parsed).
	Unknown State = iota
	// Open means the whole visit fits inside one open span.
	Open
	// ClosedAllDay means the place does not open at all that day.
	ClosedAllDay
	// OpensLater means the place is shut at the start time but opens later
	// that day; Opens is when.
	OpensLater
	// ClosesEarly means the place is open at the start time but shuts before
	// the visit would end; Closes is when.
	ClosesEarly
	// ClosedByThen means the place was open earlier that day but has shut
	// for good by the start time.
	ClosedByThen
)

func (s State) String() string {
	switch s {
	case Open:
		return "open"
	case ClosedAllDay:
		return "closed_all_day"
	case OpensLater:
		return "opens_later"
	case ClosesEarly:
		return "closes_early"
	case ClosedByThen:
		return "closed_by_then"
	default:
		return "unknown"
	}
}

// Result is the answer for one visit. Opens and Closes are minutes from
// midnight of the visit's day and describe the span the state refers to; they
// are zero for states with no span. Closes can exceed 1440 when the span runs
// past midnight.
type Result struct {
	State  State
	Opens  int
	Closes int
	// HolidayUncertain is set when the day is a public holiday and the hours
	// say nothing about holidays, so the weekday hours were used.
	HolidayUncertain bool
}

type span struct{ from, to int }

type rule struct {
	days     [7]bool
	weekdays bool // a weekday selector was given
	holiday  bool // PH was given
	closed   bool
	spans    []span
	additive bool // joined to the previous rule with "," rather than ";"
}

// matches reports whether the rule applies. With no selector at all a rule
// applies to every day.
func (r rule) matches(wd time.Weekday, holiday bool) bool {
	if !r.weekdays && !r.holiday {
		return true
	}
	if holiday && r.holiday {
		return true
	}
	return r.weekdays && r.days[wd]
}

// Schedule is a parsed opening_hours value. The zero value answers Unknown.
type Schedule struct {
	rules []rule
}

var weekdayTokens = map[string]time.Weekday{
	"su": time.Sunday, "mo": time.Monday, "tu": time.Tuesday, "we": time.Wednesday,
	"th": time.Thursday, "fr": time.Friday, "sa": time.Saturday,
}

// Parse reads raw into a Schedule, or returns an error wrapping
// ErrUnsupported.
func Parse(raw string) (Schedule, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.NewReplacer("–", "-", "—", "-").Replace(s)
	var out Schedule
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		rules, err := parsePart(part)
		if err != nil {
			return Schedule{}, fmt.Errorf("%w: %q: %s", ErrUnsupported, raw, err)
		}
		out.rules = append(out.rules, rules...)
	}
	if len(out.rules) == 0 {
		return Schedule{}, fmt.Errorf("%w: %q: empty", ErrUnsupported, raw)
	}
	return out, nil
}

// parsePart reads one ";"-separated part. Tokens are split on spaces and
// commas; a day selector that follows hours or a modifier starts a further
// rule that adds to the first ("Mo-Fr 10:00-12:00, Sa 10:00-14:00").
func parsePart(part string) ([]rule, error) {
	tokens := strings.FieldsFunc(part, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
	var (
		rules   []rule
		cur     rule
		started bool // cur has a selector or a body
		hasBody bool // cur has hours or off
	)
	flush := func() error {
		if !started {
			return nil
		}
		if !hasBody {
			return errors.New("day selector without hours")
		}
		cur.additive = len(rules) > 0
		rules = append(rules, cur)
		cur, started, hasBody = rule{}, false, false
		return nil
	}

	for _, tok := range tokens {
		switch {
		case tok == "open":
			// "Mo-Fr 09:00-17:00 open": the default modifier, no meaning.
			if !hasBody {
				return nil, errors.New("open without hours")
			}
		case tok == "off" || tok == "closed":
			if hasBody {
				return nil, errors.New("off beside hours in one rule")
			}
			cur.closed, started, hasBody = true, true, true
		case isDaySelector(tok):
			if hasBody {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			if err := addDays(&cur, tok); err != nil {
				return nil, err
			}
			started = true
		default:
			sp, err := parseSpan(tok)
			if err != nil {
				return nil, err
			}
			if cur.closed {
				return nil, errors.New("hours beside off in one rule")
			}
			cur.spans = append(cur.spans, sp)
			started, hasBody = true, true
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return rules, nil
}

// isDaySelector reports whether tok is PH or starts with a weekday. It only
// routes the token; addDays validates it.
func isDaySelector(tok string) bool {
	if tok == "ph" {
		return true
	}
	if len(tok) < 2 {
		return false
	}
	_, ok := weekdayTokens[tok[:2]]
	return ok
}

func addDays(r *rule, tok string) error {
	if tok == "ph" {
		r.holiday = true
		return nil
	}
	from, to, isRange := strings.Cut(tok, "-")
	a, ok := weekdayTokens[from]
	if !ok {
		return fmt.Errorf("day %q", tok)
	}
	if !isRange {
		r.days[a], r.weekdays = true, true
		return nil
	}
	b, ok := weekdayTokens[to]
	if !ok {
		return fmt.Errorf("day %q", tok)
	}
	r.weekdays = true
	for d := a; ; d = (d + 1) % 7 {
		r.days[d] = true
		if d == b {
			return nil
		}
	}
}

// parseSpan reads "HH:MM-HH:MM" or "24/7". An end before the start runs past
// midnight and is stored as end+1440; "24:00" is a valid end.
func parseSpan(tok string) (span, error) {
	if tok == "24/7" {
		return span{0, dayMinutes}, nil
	}
	from, to, ok := strings.Cut(tok, "-")
	if !ok {
		return span{}, fmt.Errorf("token %q", tok)
	}
	a, err := parseClock(from, false)
	if err != nil {
		return span{}, err
	}
	b, err := parseClock(to, true)
	if err != nil {
		return span{}, err
	}
	if b == a {
		return span{}, fmt.Errorf("empty span %q", tok)
	}
	if b < a {
		b += dayMinutes
	}
	return span{a, b}, nil
}

func parseClock(s string, allow24 bool) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("time %q", s)
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("time %q", s)
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if m > 59 || h > 24 || (h == 24 && (m != 0 || !allow24)) {
		return 0, fmt.Errorf("time %q", s)
	}
	return h*60 + m, nil
}

// spansFor is the day's open spans. byPH is true when a PH rule decided
// them: on a holiday the first PH rule discards the weekday hours gathered so
// far, and from then on only PH rules are heard.
func (s Schedule) spansFor(wd time.Weekday, holiday bool) (spans []span, byPH bool) {
	for _, r := range s.rules {
		if !r.matches(wd, holiday) {
			continue
		}
		isPH := holiday && r.holiday
		if byPH && !isPH {
			continue
		}
		replace := !r.additive
		if isPH && !byPH {
			byPH, replace = true, true
		}
		switch {
		case r.closed:
			spans = nil
		case replace:
			spans = append([]span(nil), r.spans...)
		default:
			spans = append(spans, r.spans...)
		}
	}
	return spans, byPH
}

// OpensOn reports whether the place has any opening hours of its own on wd:
// hours that only run over from the night before do not count. known is
// false for a schedule that was never parsed; holidayUncertain is as in
// Result.
func (s Schedule) OpensOn(wd time.Weekday, holiday bool) (opens, holidayUncertain, known bool) {
	if len(s.rules) == 0 {
		return false, false, false
	}
	today, byPH := s.spansFor(wd, holiday)
	return len(today) > 0, holiday && !byPH && len(today) > 0, true
}

// Eval checks a visit that starts startMinute minutes after midnight on wd
// and lasts durationMin minutes. holiday says the day is a public holiday.
//
// The small hours also count spans that began the day before and ran past
// midnight; that day is assumed not to be a holiday.
func (s Schedule) Eval(wd time.Weekday, startMinute, durationMin int, holiday bool) Result {
	if len(s.rules) == 0 {
		return Result{State: Unknown}
	}
	today, byPH := s.spansFor(wd, holiday)
	uncertain := holiday && !byPH && len(today) > 0

	spans := append([]span(nil), today...)
	yesterday, _ := s.spansFor((wd+6)%7, false)
	for _, sp := range yesterday {
		if sp.to > dayMinutes {
			spans = append(spans, span{0, sp.to - dayMinutes})
		}
	}
	if len(spans) == 0 {
		return Result{State: ClosedAllDay}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })

	end := startMinute + max(durationMin, 0)
	for _, sp := range spans {
		if startMinute >= sp.from && startMinute < sp.to {
			st := Open
			if end > sp.to {
				st = ClosesEarly
			}
			return Result{State: st, Opens: sp.from, Closes: sp.to, HolidayUncertain: uncertain}
		}
	}
	for _, sp := range spans {
		if sp.from > startMinute {
			return Result{State: OpensLater, Opens: sp.from, Closes: sp.to, HolidayUncertain: uncertain}
		}
	}
	return Result{State: ClosedByThen}
}
