package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/bundle"
)

// PlanGenerator turns a prompt into a pack's worth of days.
//
// It is an interface so the forge can be run dry, tested without a provider,
// and pointed at the live generation path later without changing anything
// around it.
type PlanGenerator interface {
	GeneratePlan(ctx context.Context, prompt string) (Plan, string, error)
}

// Plan is the model's answer, in the shape the prompt demands.
type Plan struct {
	Summary string    `json:"summary"`
	Days    []PlanDay `json:"days"`
}

// PlanDay is one day of a generated plan.
type PlanDay struct {
	DayNumber int        `json:"day_number"`
	Title     string     `json:"title"`
	Summary   string     `json:"summary"`
	Stops     []PlanStop `json:"stops"`
}

// PlanStop is one place.
type PlanStop struct {
	Name            string  `json:"name"`
	Category        string  `json:"category"`
	Description     string  `json:"description"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Address         string  `json:"address"`
	Website         string  `json:"website"`
	Notes           string  `json:"notes"`
	StartMinute     int     `json:"start_minute"`
	DurationMinutes int     `json:"duration_minutes"`
}

// Prompt is what the model is asked for.
//
// It asks for a pack, not a chat answer: a fixed number of days, a reason for
// every stop, and coordinates on all of them, because a stop without
// coordinates draws nothing on the map and publish refuses the pack.
func Prompt(s Seed) string {
	start, end := DayWindow(s.Theme)
	return fmt.Sprintf(`You are writing a curated %d-day travel guide for %s, themed around %s.

Return ONLY JSON matching this shape, with no prose around it:
{
  "summary": "two sentences on why this trip, at this time of year",
  "days": [
    {
      "day_number": 1,
      "title": "short title for the day",
      "summary": "one sentence on the shape of the day",
      "stops": [
        {
          "name": "exact name of the place",
          "category": "museum | restaurant | park | landmark | bar | shop | viewpoint",
          "description": "what it is, in one sentence",
          "latitude": 0.0,
          "longitude": 0.0,
          "address": "street address",
          "website": "https://... or empty string",
          "notes": "why this stop is in this guide, and what to do there",
          "start_minute": %d,
          "duration_minutes": 90
        }
      ]
    }
  ]
}

Rules:
- Exactly %d days, numbered 1 to %d.
- Four to six stops a day, in the order they should be visited.
- Every stop must be a real, currently-open place in %s with accurate coordinates.
- start_minute is minutes from midnight; every stop starts between %s and %s, and leaves travel time after the stop before it.
- notes is the reason this place earns its spot. It is what somebody is paying for, so make it specific rather than generic praise.
- Prefer places that suit %s and the season implied by "%s".`,
		s.Days, s.City, s.Theme, start, s.Days, s.Days, s.City, clock(start), clock(end), s.Theme, s.Hook)
}

// ParsePlan reads the model's answer, tolerating the fenced code block models
// wrap JSON in even when told not to.
func ParsePlan(raw string) (Plan, error) {
	txt := strings.TrimSpace(raw)
	if i := strings.Index(txt, "```"); i >= 0 {
		txt = txt[i+3:]
		if j := strings.Index(txt, "\n"); j >= 0 {
			txt = txt[j+1:]
		}
		if k := strings.LastIndex(txt, "```"); k >= 0 {
			txt = txt[:k]
		}
	}
	// Fall back to the outermost braces if there is still stray prose.
	if start, end := strings.Index(txt, "{"), strings.LastIndex(txt, "}"); start >= 0 && end > start {
		txt = txt[start : end+1]
	}

	var p Plan
	if err := json.Unmarshal([]byte(txt), &p); err != nil {
		return Plan{}, fmt.Errorf("parse plan json: %w", err)
	}
	if len(p.Days) == 0 {
		return Plan{}, fmt.Errorf("plan has no days")
	}
	unescape(&p)
	return p, nil
}

// unescape undoes HTML entities some models put in plain JSON strings. The
// text is stored and rendered as text, so "&amp;" reached the page literally.
func unescape(p *Plan) {
	p.Summary = html.UnescapeString(p.Summary)
	for i := range p.Days {
		d := &p.Days[i]
		d.Title = html.UnescapeString(d.Title)
		d.Summary = html.UnescapeString(d.Summary)
		for j := range d.Stops {
			st := &d.Stops[j]
			st.Name = html.UnescapeString(st.Name)
			st.Description = html.UnescapeString(st.Description)
			st.Address = html.UnescapeString(st.Address)
			st.Notes = html.UnescapeString(st.Notes)
		}
	}
}

// ToDraft converts a generated plan into the rows a draft is made of.
//
// Day numbers are renumbered from one by position rather than trusted: models
// skip, repeat and zero-index them, and a pack numbered "Day 2 to Day 5" reads
// as broken even when the content is right.
func ToDraft(s Seed, p Plan, model string, cityID *uuid.UUID, paid bool) bundle.Draft {
	months := make([]int16, 0, len(s.Months))
	for _, m := range s.Months {
		months = append(months, int16(m))
	}

	days := make([]bundle.Day, 0, len(p.Days))
	for i, d := range p.Days {
		stops := make([]bundle.Stop, 0, len(d.Stops))
		for j, st := range d.Stops {
			stop := bundle.Stop{
				OrderIndex:  j,
				Name:        strings.TrimSpace(st.Name),
				Category:    st.Category,
				Description: st.Description,
				Address:     st.Address,
				Notes:       st.Notes,
			}
			if st.Latitude != 0 || st.Longitude != 0 {
				lat, lon := st.Latitude, st.Longitude
				stop.Latitude, stop.Longitude = &lat, &lon
			}
			if w := strings.TrimSpace(st.Website); w != "" {
				stop.Website = &w
			}
			if st.StartMinute > 0 {
				v := st.StartMinute
				stop.StartMinute = &v
			}
			if st.DurationMinutes > 0 {
				v := st.DurationMinutes
				stop.DurationMinutes = &v
			}
			stops = append(stops, stop)
		}
		days = append(days, bundle.Day{
			DayNumber: i + 1,
			Title:     d.Title,
			Summary:   d.Summary,
			Stops:     stops,
		})
	}

	return bundle.Draft{
		Slug:         s.Slug(),
		Title:        s.Title(),
		Summary:      p.Summary,
		CityID:       cityID,
		CityName:     s.City,
		CountryCode:  s.CountryCode,
		Theme:        s.Theme,
		Months:       months,
		IsPaid:       paid,
		SourcePrompt: Prompt(s),
		SourceModel:  model,
		Days:         days,
	}
}
