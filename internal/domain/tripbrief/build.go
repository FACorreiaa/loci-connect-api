package tripbrief

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/push"
)

// maxBodyRunes keeps the banner to two lines.
const maxBodyRunes = 178

// Weather turn thresholds: dry at breakfast, wet now.
const (
	dryBelow = 0.4
	wetFrom  = 0.6
)

const sep = " · "

func tripURL(c Candidate) string { return push.WebOrigin + "/trips/" + c.TripID.String() }

func dayTitle(c Candidate) string {
	if c.City == "" {
		return fmt.Sprintf("Day %d of your trip", c.DayNumber)
	}
	return fmt.Sprintf("Day %d in %s", c.DayNumber, c.City)
}

var conditionWords = map[string]string{
	"clear": "clear", "clouds": "cloudy", "rain": "rain", "drizzle": "drizzle",
	"snow": "snow", "thunderstorm": "storms", "mist": "fog", "fog": "fog", "haze": "haze",
}

func weatherPiece(wx *localcontext.WeatherDay) string {
	cond := strings.ToLower(strings.TrimSpace(wx.Condition))
	if w, ok := conditionWords[cond]; ok {
		cond = w
	}
	high := fmt.Sprintf("%d°C", int(math.Round(wx.HighC)))
	if cond == "" {
		return high
	}
	return high + ", " + cond
}

func stopsPiece(stops []Stop) string {
	n := "1 stop"
	if len(stops) != 1 {
		n = fmt.Sprintf("%d stops", len(stops))
	}
	first := strings.TrimSpace(stops[0].Name)
	if first == "" {
		return n
	}
	piece := n + ", first " + first
	if m := stops[0].StartMinute; m != nil && *m >= 0 && *m < 24*60 {
		piece += fmt.Sprintf(" at %02d:%02d", *m/60, *m%60)
	}
	return piece
}

func closedPiece(c Candidate, stops []Stop) string {
	var names []string
	for _, s := range stops {
		if closed, known := ClosedOn(s.OpeningHours, c.LocalDate.Weekday()); known && closed {
			names = append(names, strings.TrimSpace(s.Name))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "Closed today: " + strings.Join(names, ", ")
}

// joinCapped joins pieces until the cap, cutting the piece that crosses it
// when enough of it would still read.
func joinCapped(pieces []string, limit int) string {
	var b strings.Builder
	used := 0
	for _, p := range pieces {
		if p == "" {
			continue
		}
		add := p
		if used > 0 {
			add = sep + p
		}
		n := utf8.RuneCountInString(add)
		if used+n <= limit {
			b.WriteString(add)
			used += n
			continue
		}
		room := limit - used - 1 // for the ellipsis
		if room >= 12 {
			r := []rune(add)
			b.WriteString(strings.TrimRight(string(r[:room]), " ,·") + "…")
		}
		break
	}
	return b.String()
}

// BuildMorning is the 8:00 brief. ok is false when there is neither weather
// nor a planned stop to talk about.
func BuildMorning(c Candidate, stops []Stop, wx *localcontext.WeatherDay, alerts []localcontext.Alert) (Brief, bool) {
	if wx == nil && len(stops) == 0 {
		return Brief{}, false
	}
	var pieces []string
	if wx != nil {
		pieces = append(pieces, weatherPiece(wx))
	}
	if len(stops) > 0 {
		pieces = append(pieces, stopsPiece(stops), closedPiece(c, stops))
	}
	for _, a := range alerts {
		if t := strings.TrimSpace(a.Title); t != "" {
			pieces = append(pieces, t)
			break
		}
	}
	return Brief{Title: dayTitle(c), Body: joinCapped(pieces, maxBodyRunes), URL: tripURL(c)}, true
}

// BuildWeatherTurn is the heads-up when a day that was dry at breakfast is
// now likely wet.
func BuildWeatherTurn(c Candidate, morning, now float32) (Brief, bool) {
	if morning >= dryBelow || now < wetFrom {
		return Brief{}, false
	}
	title := "Rain likely today"
	if c.City != "" {
		title = "Rain likely in " + c.City
	}
	return Brief{
		Title: title,
		Body:  "The afternoon turned wet — your trip has indoor options.",
		URL:   tripURL(c),
	}, true
}
