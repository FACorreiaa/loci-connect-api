package tripbrief

import (
	"encoding/json"
	"strings"
	"time"
)

var dayTokens = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// ClosedOn says whether a place is closed all day on wd. known is false
// whenever the hours are in a form this does not read, or do not mention
// that day: a brief would rather say nothing than call an open place closed.
//
// It reads the canonical place-facts form ("mon-fri 09:00-17:00; sun
// closed"), that form as a JSON string, and a JSON object keyed by day name
// ({"monday": "Closed"}).
func ClosedOn(hours string, wd time.Weekday) (closed, known bool) {
	hours = strings.TrimSpace(hours)
	switch {
	case hours == "":
		return false, false
	case strings.HasPrefix(hours, `"`):
		var s string
		if json.Unmarshal([]byte(hours), &s) != nil {
			return false, false
		}
		return closedOnCanonical(s, wd)
	case strings.HasPrefix(hours, "{"):
		var m map[string]any
		if json.Unmarshal([]byte(hours), &m) != nil {
			return false, false
		}
		for k, v := range m {
			key := strings.ToLower(strings.TrimSpace(k))
			if len(key) < 3 {
				continue
			}
			if d, ok := dayTokens[key[:3]]; ok && d == wd {
				s, _ := v.(string)
				return strings.Contains(strings.ToLower(s), "closed"), true
			}
		}
		return false, false
	}
	return closedOnCanonical(hours, wd)
}

func closedOnCanonical(hours string, wd time.Weekday) (closed, known bool) {
	for _, seg := range strings.Split(strings.ToLower(hours), ";") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		spec, rest, _ := strings.Cut(seg, " ")
		days, ok := parseDays(spec)
		if !ok {
			return false, false
		}
		if days[wd] {
			return strings.TrimSpace(rest) == "closed", true
		}
	}
	return false, false
}

// parseDays reads "mon", "mon-fri", "sun-mon" (wrapping) and comma lists.
func parseDays(spec string) (days [7]bool, ok bool) {
	for _, tok := range strings.Split(spec, ",") {
		from, to, isRange := strings.Cut(tok, "-")
		a, okA := dayTokens[from]
		if !okA {
			return days, false
		}
		if !isRange {
			days[a] = true
			continue
		}
		b, okB := dayTokens[to]
		if !okB {
			return days, false
		}
		for d := a; ; d = (d + 1) % 7 {
			days[d] = true
			if d == b {
				break
			}
		}
	}
	return days, true
}
