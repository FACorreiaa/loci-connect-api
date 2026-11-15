package tripaction

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

// resolveStopEdit turns an extracted stop edit (day numbers, a stop's name,
// a place to look up) into the id form the trip page sends, and writes the
// card's summary. false means the edit names nothing on the trip, or a place
// that can't be found; no card is offered for it.
func (s *Service) resolveStopEdit(ctx context.Context, t *trip.Trip, p *Proposal) bool {
	a := &p.Action
	day := dayNumbered(t, a.Day)
	if day == nil {
		return false
	}
	if a.Kind != KindAddStop {
		stop := stopNamed(t, day, a.StopName)
		if stop == nil {
			return false
		}
		a.StopID = stop.ID.String()
	}
	switch a.Kind {
	case KindAddStop:
		a.DayID = day.ID.String()
		fallthrough
	case KindReplaceStop:
		stop, ok := s.lookUpStop(ctx, a.PlaceQuery, cityOf(t, day))
		if !ok {
			return false
		}
		a.Stop = stop
	case KindMoveStop:
		to := dayNumbered(t, a.ToDay)
		if to == nil {
			return false
		}
		a.ToDayID, a.ToIndex = to.ID.String(), len(to.Stops)
	}
	summary, err := checkStopEdit(*a, t)
	if err != nil {
		return false
	}
	p.Summary = summary
	return true
}

// lookUpStop is the place the city search finds for query, as a stop.
func (s *Service) lookUpStop(ctx context.Context, query, city string) (*trip.TripStop, bool) {
	if s.d.Spots == nil || query == "" || city == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, spotTimeout)
	defer cancel()
	found, err := s.d.Spots.SearchPOIsByQueryAndCity(ctx, query, city)
	if err != nil {
		s.d.Logger.WarnContext(ctx, "trip action: place search failed", slog.String("query", query), slog.Any("error", err))
		return nil, false
	}
	names := make([]string, len(found))
	for i := range found {
		names[i] = found[i].Name
	}
	i := pickSpot(query, names)
	if i < 0 {
		return nil, false
	}
	f := found[i]
	stop := &trip.TripStop{Name: f.Name, Notes: firstNonEmpty(f.DescriptionPOI, f.Description, "Suggested in chat")}
	if f.ID != uuid.Nil {
		stop.POIID = f.ID.String()
	}
	return stop, true
}

// pickSpot is which search result a message meant, or -1 when none has a
// name.
//
// The search ranks by meaning, so asking for a place by name often puts a
// similar place first: "Time Out Market" finds a food hall two streets away
// ahead of the market itself. A result whose name is what was asked for wins,
// then one whose name contains it, and only then the search's own first
// result, which is the right answer when the message described a kind of
// place ("a coffee shop") rather than naming one.
func pickSpot(query string, names []string) int {
	want := strings.ToLower(strings.TrimSpace(query))
	first, contains := -1, -1
	for i, name := range names {
		got := strings.ToLower(strings.TrimSpace(name))
		if got == "" {
			continue
		}
		if got == want {
			return i
		}
		if contains < 0 && want != "" && strings.Contains(got, want) {
			contains = i
		}
		if first < 0 {
			first = i
		}
	}
	if contains >= 0 {
		return contains
	}
	return first
}

func dayNumbered(t *trip.Trip, n int) *trip.TripDay {
	for i := range t.Days {
		if int(t.Days[i].DayNumber) == n {
			return &t.Days[i]
		}
	}
	return nil
}

// stopNamed finds the stop a message names: on its day first, then anywhere
// on the trip (the model may have the day wrong), exact before partial.
func stopNamed(t *trip.Trip, day *trip.TripDay, name string) *trip.TripStop {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	match := func(stops []trip.TripStop, partial bool) *trip.TripStop {
		for i := range stops {
			got := strings.ToLower(strings.TrimSpace(stops[i].Name))
			if got == name || partial && got != "" && (strings.Contains(got, name) || strings.Contains(name, got)) {
				return &stops[i]
			}
		}
		return nil
	}
	for _, partial := range []bool{false, true} {
		if st := match(day.Stops, partial); st != nil {
			return st
		}
		for i := range t.Days {
			if st := match(t.Days[i].Stops, partial); st != nil {
				return st
			}
		}
	}
	return nil
}

func cityOf(t *trip.Trip, day *trip.TripDay) string {
	if day.CityName != "" {
		return day.CityName
	}
	return t.CityName
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// proposed tells a trip's owner what a co-planner suggested in chat, in one
// push, and records the together event for each card.
func (s *Service) proposed(ctx context.Context, userID uuid.UUID, t *trip.Trip, owner bool, ps []Proposal) {
	if len(ps) == 0 || !s.together() {
		return
	}
	shared := !owner
	if owner {
		// The owner's own cards count as planning together only when someone
		// else is on the trip to see them.
		everyone, err := s.d.Members.Everyone(ctx, t)
		shared = err == nil && len(everyone) > 1
	}
	if shared {
		for _, p := range ps {
			s.capture(userID, "trip_proposal_created", map[string]any{
				"trip_id": t.ID.String(), "source": string(SourceChat), "kind": string(p.Action.Kind),
			})
		}
	}
	if owner {
		return
	}
	body := s.nameOf(ctx, userID) + " suggested: " + ps[0].Summary
	if len(ps) > 1 {
		body += fmt.Sprintf(" (and %d more)", len(ps)-1)
	}
	s.notify(ctx, []uuid.UUID{t.UserID}, t, body)
}
