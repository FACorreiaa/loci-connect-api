package tripspan

import (
	"regexp"
	"strconv"
	"time"
)

// A date range beats a spelled-out duration: in "5 days in Madeira, 3-9 May"
// the number is the traveller's estimate and the dates are their booking.
//
// The shapes below are deliberately narrow. An unrecognised range falls through
// to the duration rules and, failing those, to the default — a miss costs a
// two-day sample, whereas a loose pattern that reads "Room 12-15" as a range
// costs a wrong answer that looks confident.

const (
	// sep is what sits between the two ends of a range. "to" and friends need
	// spaces around them; the dashes do not.
	sep = `(?:\s*[-\x{2013}\x{2014}]\s*|\s+(?:to|until|till|through)\s+)`
	// ord swallows the "rd" in "3rd".
	ord = `(?:st|nd|rd|th)?`
	mon = `(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t)?(?:ember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)`
)

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September,
	"oct": time.October, "nov": time.November, "dec": time.December,
}

func monthOf(s string) time.Month {
	if len(s) < 3 {
		return 0
	}
	return months[s[:3]]
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

var (
	reISO   = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})` + sep + `(\d{4})-(\d{2})-(\d{2})`)
	reSlash = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{4}))?` + sep + `(\d{1,2})/(\d{1,2})(?:/(\d{4}))?\b`)
	// "3 May to 9 May 2026", "28 Dec to 3 Jan"
	reDayMonPair = regexp.MustCompile(`\b(\d{1,2})` + ord + `\s+` + mon + `(?:\s+(\d{4}))?` + sep + `(\d{1,2})` + ord + `\s+` + mon + `(?:\s+(\d{4}))?`)
	// "3-9 May 2026" — one month, shared
	reDayRangeMon = regexp.MustCompile(`\b(\d{1,2})` + ord + sep + `(\d{1,2})` + ord + `\s+` + mon + `(?:\s+(\d{4}))?`)
	// "May 3-9"
	reMonDayRange = regexp.MustCompile(`\b` + mon + `\s+(\d{1,2})` + ord + sep + `(\d{1,2})` + ord + `\b`)
)

// parseRange finds the first range shape that matches and turns it into a span.
func parseRange(now time.Time, t string) (Span, bool) {
	if m := reISO.FindStringSubmatch(t); m != nil {
		start := date(atoi(m[1]), time.Month(atoi(m[2])), atoi(m[3]))
		end := date(atoi(m[4]), time.Month(atoi(m[5])), atoi(m[6]))
		return spanBetween(start, end, false, m[0])
	}

	if m := reSlash.FindStringSubmatch(t); m != nil {
		// Day-first: see the package comment.
		sy, ey := yearsFor(now, m[3], m[6])
		start := date(sy, time.Month(atoi(m[2])), atoi(m[1]))
		end := date(ey, time.Month(atoi(m[5])), atoi(m[4]))
		return spanBetween(start, end, m[3] == "" && m[6] == "", m[0])
	}

	if m := reDayMonPair.FindStringSubmatch(t); m != nil {
		sy, ey := yearsFor(now, m[3], m[6])
		start := date(sy, monthOf(m[2]), atoi(m[1]))
		end := date(ey, monthOf(m[5]), atoi(m[4]))
		return spanBetween(start, end, m[3] == "" && m[6] == "", m[0])
	}

	if m := reDayRangeMon.FindStringSubmatch(t); m != nil {
		y, _ := yearsFor(now, m[4], m[4])
		month := monthOf(m[3])
		return spanBetween(date(y, month, atoi(m[1])), date(y, month, atoi(m[2])), m[4] == "", m[0])
	}

	if m := reMonDayRange.FindStringSubmatch(t); m != nil {
		y := now.Year()
		month := monthOf(m[1])
		return spanBetween(date(y, month, atoi(m[2])), date(y, month, atoi(m[3])), true, m[0])
	}

	return Span{}, false
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// yearsFor fills in the years a bare range does not state. An explicit year on
// either end is used for both, so "3 May to 9 May 2026" is not half in this
// year.
func yearsFor(now time.Time, startYear, endYear string) (int, int) {
	switch {
	case startYear != "" && endYear != "":
		return atoi(startYear), atoi(endYear)
	case startYear != "":
		return atoi(startYear), atoi(startYear)
	case endYear != "":
		return atoi(endYear), atoi(endYear)
	default:
		return now.Year(), now.Year()
	}
}

// spanBetween turns two dates into an inclusive day count.
//
// When neither end stated a year and the range runs backwards, it is a turn of
// the year: "28 Dec to 3 Jan" is seven days, not minus three hundred. The
// rolled result is only accepted if it is a plausible trip, which is what stops
// "9 May to 3 May" — a typo, or two unrelated numbers — from becoming an
// eleven-month holiday. A rejected range falls through to the duration rules.
func spanBetween(start, end time.Time, yearsInferred bool, match string) (Span, bool) {
	rolled := false
	if end.Before(start) {
		if !yearsInferred {
			return Span{}, false
		}
		end = end.AddDate(1, 0, 0)
		rolled = true
	}

	days := int(end.Sub(start).Hours()/24) + 1
	if days < 1 {
		return Span{}, false
	}
	if rolled && days > MaxDays {
		return Span{}, false
	}

	clamped, wasClamped := clamp(days)
	return Span{Days: clamped, Source: SourceDateRange, Match: match, Clamped: wasClamped}, true
}
