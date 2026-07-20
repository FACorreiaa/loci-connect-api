package trip

import (
	"fmt"
	"strings"
	"time"
)

// Proto TripPace values (loci.trip.TripPace).
const (
	paceRelaxed  int32 = 1
	paceModerate int32 = 2
	pacePacked   int32 = 3
)

// buildTripMarkdown renders a day-by-day itinerary as Markdown (Premium export).
func buildTripMarkdown(t *Trip) string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = "Trip"
	}
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	if t.CityName != "" {
		fmt.Fprintf(&b, "**City:** %s\n", t.CityName)
	}
	if pace := paceLabel(t.Constraints.Pace); pace != "" {
		fmt.Fprintf(&b, "**Pace:** %s\n", pace)
	}
	if strings.TrimSpace(t.Constraints.Mobility) != "" {
		fmt.Fprintf(&b, "**Mobility:** %s\n", t.Constraints.Mobility)
	}
	if len(t.Constraints.Interests) > 0 {
		fmt.Fprintf(&b, "**Interests:** %s\n", strings.Join(t.Constraints.Interests, ", "))
	}
	fmt.Fprintf(&b, "\n*Exported from Loci · %s*\n\n", time.Now().UTC().Format(time.RFC3339))

	for _, d := range t.Days {
		fmt.Fprintf(&b, "## Day %d", d.DayNumber)
		if d.Date != nil {
			fmt.Fprintf(&b, " — %s", d.Date.Format("2006-01-02"))
		}
		b.WriteString("\n\n")
		if len(d.Stops) == 0 {
			b.WriteString("_No stops._\n\n")
			continue
		}
		stops := append([]TripStop(nil), d.Stops...)
		sortStopsByOrder(stops)
		for _, s := range stops {
			name := strings.TrimSpace(s.Name)
			if name == "" {
				name = "Stop"
			}
			if s.StartMinute != nil {
				h := *s.StartMinute / 60
				m := *s.StartMinute % 60
				fmt.Fprintf(&b, "- **%02d:%02d** %s", h, m, name)
			} else {
				fmt.Fprintf(&b, "- %s", name)
			}
			if s.DurationMinutes != nil && *s.DurationMinutes > 0 {
				fmt.Fprintf(&b, " (%d min)", *s.DurationMinutes)
			}
			b.WriteByte('\n')
			if strings.TrimSpace(s.Notes) != "" {
				fmt.Fprintf(&b, "  - _Why this:_ %s\n", strings.TrimSpace(s.Notes))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func paceLabel(p int32) string {
	switch p {
	case paceRelaxed:
		return "Relaxed"
	case paceModerate:
		return "Moderate"
	case pacePacked:
		return "Packed"
	default:
		return ""
	}
}
