package forge

import (
	"fmt"
	"strings"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/bundle"
)

// Render writes a pack as markdown for a person to read before approving it.
//
// This is the approval surface. It is a terminal document rather than a screen
// because reading a day in order is the only way to judge whether the writing
// is worth money, and because an admin UI would need an authorisation system
// that does not exist yet.
func Render(b *bundle.Bundle, days []bundle.Day) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# %s\n\n", b.Title)
	fmt.Fprintf(&sb, "slug: %s\nstatus: %s\ncity: %s (%s)\ntheme: %s\nmonths: %v\npaid: %t\nmodel: %s\n\n",
		b.Slug, b.Status, b.CityName, b.CountryCode, b.Theme, b.Months, b.IsPaid, b.SourceModel)
	if b.Summary != "" {
		fmt.Fprintf(&sb, "%s\n\n", b.Summary)
	}

	for _, d := range days {
		fmt.Fprintf(&sb, "## Day %d — %s\n", d.DayNumber, d.Title)
		if d.Summary != "" {
			fmt.Fprintf(&sb, "_%s_\n", d.Summary)
		}
		sb.WriteString("\n")

		for _, s := range d.Stops {
			when := ""
			if s.StartMinute != nil {
				when = fmt.Sprintf("%02d:%02d ", *s.StartMinute/60, *s.StartMinute%60)
			}
			dur := ""
			if s.DurationMinutes != nil {
				dur = fmt.Sprintf(" (%dm)", *s.DurationMinutes)
			}
			fmt.Fprintf(&sb, "- **%s%s**%s — %s\n", when, s.Name, dur, s.Category)
			if s.Notes != "" {
				fmt.Fprintf(&sb, "  - %s\n", s.Notes)
			}
			if s.Address != "" {
				fmt.Fprintf(&sb, "  - %s\n", s.Address)
			}
			// Problems are shown inline, where the reader is already looking,
			// rather than in a summary they have to cross-reference.
			if s.Latitude == nil || s.Longitude == nil {
				sb.WriteString("  - ⚠ NO COORDINATES — will not draw on the map; publish will refuse\n")
			}
			if s.POIID == nil {
				sb.WriteString("  - (no linked POI row)\n")
			}
		}
		sb.WriteString("\n")
	}

	if issues := Validate(b, days); len(issues) > 0 {
		sb.WriteString("## Blocking issues\n\n")
		for _, p := range issues {
			fmt.Fprintf(&sb, "- %s\n", p)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// Validate reports what would make this pack a broken product. Publish refuses
// while anything here is outstanding.
//
// The coordinate rule is the important one: a paid guide whose map is empty is
// not a guide, and the client's map link builder correctly returns nothing for
// a stop with no position, so the failure is silent in the UI.
func Validate(b *bundle.Bundle, days []bundle.Day) []string {
	var issues []string

	if len(days) == 0 {
		issues = append(issues, "pack has no days")
	}
	if b.DayCount != len(days) {
		issues = append(issues,
			fmt.Sprintf("day_count is %d but %d days are stored", b.DayCount, len(days)))
	}
	if strings.TrimSpace(b.Summary) == "" {
		issues = append(issues, "pack has no summary; the catalog card would be blank")
	}

	for _, d := range days {
		if len(d.Stops) == 0 {
			issues = append(issues, fmt.Sprintf("day %d has no stops", d.DayNumber))
			continue
		}
		for _, s := range d.Stops {
			if strings.TrimSpace(s.Name) == "" {
				issues = append(issues, fmt.Sprintf("day %d has a stop with no name", d.DayNumber))
			}
			if s.Latitude == nil || s.Longitude == nil {
				issues = append(issues, fmt.Sprintf(
					"day %d: %q has no coordinates", d.DayNumber, s.Name,
				))
			}
			if strings.TrimSpace(s.Notes) == "" {
				issues = append(issues, fmt.Sprintf(
					"day %d: %q has no notes, which is the part being paid for", d.DayNumber, s.Name,
				))
			}
		}
	}
	return issues
}
