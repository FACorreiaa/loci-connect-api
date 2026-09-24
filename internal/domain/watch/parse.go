package watch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Parsing is rule-based on purpose: a proposal is shown to the user before
// anything is stored, so it has to be cheap, instant and predictable. The LLM
// is spent on running a watch, not on reading its schedule.

const (
	defaultHour   = 9
	maxTitleRunes = 60
	maxSpecRunes  = 2000
)

var (
	reAtTime  = regexp.MustCompile(`\b(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\b|\bat\s+(\d{1,2})(?::(\d{2}))?\b(?:\s*(?:o'?clock|h))?`)
	reEveryN  = regexp.MustCompile(`\bevery\s+(\d{1,3})\s+(minutes?|mins?|hours?|hrs?|days?|weeks?)\b`)
	reHourly  = regexp.MustCompile(`\b(?:every\s+hour|hourly|each\s+hour)\b`)
	reWeekly  = regexp.MustCompile(`\b(?:every\s+week|weekly|each\s+week|once\s+a\s+week)\b`)
	reDaily   = regexp.MustCompile(`\b(?:every\s+day|daily|each\s+day|once\s+a\s+day)\b`)
	rePartOf  = regexp.MustCompile(`\b(?:every|each)\s+(morning|afternoon|evening|night)\b`)
	reWeekday = regexp.MustCompile(`\b(?:every|each|on)\s+(monday|tuesday|wednesday|thursday|friday|saturday|sunday)s?\b`)

	// Lead-ins that are about the reminder, not the task.
	reLeadIn = regexp.MustCompile(`^(?:(?:please|can you|could you|and|then|to|,|-)\s+)+`)
)

var partOfDayHour = map[string]int{
	"morning":   8,
	"afternoon": 14,
	"evening":   18,
	"night":     21,
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
}

// Propose reads a schedule and a task out of text. Times are in loc; now is
// the reference for the first run. Text with no recognisable schedule becomes
// a daily watch at 09:00.
func Propose(text string, loc *time.Location, now time.Time) (Proposal, error) {
	if loc == nil {
		loc = time.UTC
	}
	raw := strings.TrimSpace(text)
	if raw == "" {
		return Proposal{}, fmt.Errorf("%w: empty text", ErrInvalid)
	}
	lower := strings.ToLower(raw)

	var (
		interval = 0 // minutes
		hour     = -1
		minute   = 0
		weekday  = time.Weekday(-1)
		spans    [][]int
	)
	mark := func(loc []int) { spans = append(spans, loc[:2]) }

	if m := reEveryN.FindStringSubmatchIndex(lower); m != nil {
		n, _ := strconv.Atoi(lower[m[2]:m[3]])
		if n < 1 {
			n = 1
		}
		switch unit := lower[m[4]:m[5]]; {
		case strings.HasPrefix(unit, "min"):
			interval = n
		case strings.HasPrefix(unit, "h"):
			interval = n * 60
		case strings.HasPrefix(unit, "d"):
			interval = n * 1440
		case strings.HasPrefix(unit, "w"):
			interval = n * 10080
		}
		mark(m)
	}
	if m := reHourly.FindStringIndex(lower); m != nil && interval == 0 {
		interval = 60
		mark(m)
	}
	if m := reWeekday.FindStringSubmatchIndex(lower); m != nil {
		weekday = weekdays[lower[m[2]:m[3]]]
		if interval == 0 {
			interval = 10080
		}
		mark(m)
	}
	if m := reWeekly.FindStringIndex(lower); m != nil {
		if interval == 0 {
			interval = 10080
		}
		mark(m)
	}
	if m := rePartOf.FindStringSubmatchIndex(lower); m != nil {
		hour = partOfDayHour[lower[m[2]:m[3]]]
		if interval == 0 {
			interval = 1440
		}
		mark(m)
	}
	if m := reDaily.FindStringIndex(lower); m != nil {
		if interval == 0 {
			interval = 1440
		}
		mark(m)
	}
	if m := reAtTime.FindStringSubmatchIndex(lower); m != nil {
		h, mm, ok := clockFrom(lower, m)
		if ok {
			hour, minute = h, mm
			mark(m)
		}
	}

	if interval == 0 {
		interval = 1440
	}
	if interval < MinIntervalMinutes {
		interval = MinIntervalMinutes
	}
	if interval > MaxIntervalMinutes {
		interval = MaxIntervalMinutes
	}

	// A time of day only means something for schedules of a day or longer.
	daily := interval%1440 == 0
	if daily && hour < 0 {
		hour = defaultHour
	}
	if !daily {
		hour, weekday = -1, -1
	}

	spec := clean(removeSpans(raw, spans))
	if spec == "" {
		return Proposal{}, fmt.Errorf("%w: no task left once the schedule is read", ErrInvalid)
	}
	spec = truncateRunes(spec, maxSpecRunes)

	p := Proposal{
		Title:           titleFrom(spec),
		ScheduleHuman:   humanSchedule(interval, hour, minute, weekday),
		IntervalMinutes: interval,
		Spec:            spec,
	}
	if hour >= 0 {
		first := firstRun(now.In(loc), hour, minute, weekday)
		p.FirstRunAt = &first
	}
	return p, nil
}

func clockFrom(s string, m []int) (hour, minute int, ok bool) {
	group := func(i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return s[m[2*i]:m[2*i+1]]
	}
	hStr, mStr, ampm := group(1), group(2), group(3)
	if hStr == "" {
		hStr, mStr = group(4), group(5)
	}
	h, err := strconv.Atoi(hStr)
	if err != nil {
		return 0, 0, false
	}
	if mStr != "" {
		minute, _ = strconv.Atoi(mStr)
	}
	switch ampm {
	case "am":
		if h == 12 {
			h = 0
		}
	case "pm":
		if h < 12 {
			h += 12
		}
	}
	if h > 23 || minute > 59 {
		return 0, 0, false
	}
	return h, minute, true
}

func firstRun(now time.Time, hour, minute int, weekday time.Weekday) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if weekday >= 0 {
		t = t.AddDate(0, 0, (int(weekday)-int(t.Weekday())+7)%7)
		if !t.After(now) {
			t = t.AddDate(0, 0, 7)
		}
		return t
	}
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func humanSchedule(interval, hour, minute int, weekday time.Weekday) string {
	at := ""
	if hour >= 0 {
		at = fmt.Sprintf(" at %02d:%02d", hour, minute)
	}
	switch {
	case interval == 60:
		return "Every hour"
	case interval%1440 != 0 && interval%60 == 0:
		return fmt.Sprintf("Every %d hours", interval/60)
	case interval%1440 != 0:
		return fmt.Sprintf("Every %d minutes", interval)
	case interval == 1440:
		return "Every day" + at
	case interval == 10080 && weekday >= 0:
		return "Every " + weekday.String() + at
	case interval == 10080:
		return "Every week" + at
	case interval%10080 == 0:
		return fmt.Sprintf("Every %d weeks%s", interval/10080, at)
	default:
		return fmt.Sprintf("Every %d days%s", interval/1440, at)
	}
}

// removeSpans cuts the schedule phrases out of text. Spans index the
// lower-cased copy, which has the same byte layout for the ASCII the
// patterns match.
func removeSpans(text string, spans [][]int) string {
	if len(spans) == 0 || len(strings.ToLower(text)) != len(text) {
		return text
	}
	keep := make([]bool, len(text))
	for i := range keep {
		keep[i] = true
	}
	for _, sp := range spans {
		for i := sp[0]; i < sp[1] && i < len(keep); i++ {
			keep[i] = false
		}
	}
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if keep[i] {
			b.WriteByte(text[i])
		} else if i == 0 || keep[i-1] {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.Trim(s, " ,.;:-")
	for {
		loc := reLeadIn.FindStringIndex(strings.ToLower(s))
		if loc == nil || loc[1] == 0 || loc[1] > len(s) {
			break
		}
		s = strings.TrimSpace(s[loc[1]:])
	}
	return strings.Trim(s, " ,.;:-")
}

func titleFrom(spec string) string {
	t := truncateRunes(spec, maxTitleRunes)
	if t != spec {
		if i := strings.LastIndex(t, " "); i > maxTitleRunes/2 {
			t = t[:i]
		}
		t = strings.TrimRight(t, " ,.;:-") + "…"
	}
	r, size := utf8.DecodeRuneInString(t)
	return string(unicode.ToUpper(r)) + t[size:]
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
