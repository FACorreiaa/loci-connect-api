package calendar

import (
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// OverlayEvent is a calendar block derived from a Loci trip day. Provider
// events (Google, Calendly) are merged in the handler; this type is only Loci.
type OverlayEvent struct {
	ID       string
	Title    string
	Start    time.Time
	End      time.Time
	Location string
	TripID   string
}

// EventsFromTrips turns dated trip days into overlay events. Days without a
// date are skipped — they belong on the Unscheduled rail, not the month grid.
func EventsFromTrips(trips []*trip.Trip, from, to time.Time) []OverlayEvent {
	var out []OverlayEvent
	for _, t := range trips {
		if t == nil {
			continue
		}
		for _, d := range t.Days {
			if d.Date == nil {
				continue
			}
			start, end := dayWindow(d)
			if end.Before(from) || !start.Before(to) {
				continue
			}
			title := t.Title
			if title == "" {
				if t.CityName != "" {
					title = "Trip to " + t.CityName
				} else {
					title = "Untitled trip"
				}
			}
			loc := d.CityName
			if loc == "" {
				loc = t.CityName
			}
			out = append(out, OverlayEvent{
				ID:       d.ID.String(),
				Title:    title,
				Start:    start,
				End:      end,
				Location: loc,
				TripID:   t.ID.String(),
			})
		}
	}
	return out
}

func dayWindow(d trip.TripDay) (time.Time, time.Time) {
	date := *d.Date
	startMin := 9 * 60
	dur := 8 * 60
	if len(d.Stops) > 0 {
		if d.Stops[0].StartMinute != nil {
			startMin = int(*d.Stops[0].StartMinute)
		}
		if last := d.Stops[len(d.Stops)-1]; last.StartMinute != nil {
			endMin := int(*last.StartMinute)
			if last.DurationMinutes != nil {
				endMin += int(*last.DurationMinutes)
			} else {
				endMin += 60
			}
			if endMin > startMin {
				dur = endMin - startMin
			}
		}
	}
	y, m, day := date.Date()
	start := time.Date(y, m, day, startMin/60, startMin%60, 0, 0, date.Location())
	return start, start.Add(time.Duration(dur) * time.Minute)
}
