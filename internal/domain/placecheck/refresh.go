package placecheck

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Refresh asks the map again about a plan's places ahead of anyone reading
// it, and waits for each answer. It returns how many times it went to the
// map.
//
// It asks only about a place never looked up, or one that was found and
// whose answer has aged out. A place the map did not have is left alone,
// however old that answer: a plan in a city the map does not cover is all
// such places, and asking again changes nothing. A reader's own lookup still
// asks about those in its time.
//
// A day already past is skipped, as is a day with no known centre. allow is
// called before every visit to the map; false ends the refresh there, which
// is how a caller holds it to a pace and a ceiling.
func (p *Planner) Refresh(ctx context.Context, days []Day, allow func() bool) int {
	var ids []string
	for _, d := range days {
		for _, s := range d.Stops {
			if s.POIID != "" {
				ids = append(ids, s.POIID)
			}
		}
	}
	if len(ids) == 0 {
		return 0
	}
	held, err := p.svc.store.Get(ctx, ids)
	if err != nil {
		p.log.WarnContext(ctx, "placecheck: refresh could not read held checks", slog.Any("error", err))
		return 0
	}
	now := p.svc.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	asked := map[string]bool{}
	visits := 0
	for _, d := range days {
		if !d.HasCentre || (d.Date != nil && d.Date.Before(today)) {
			continue
		}
		var due []POI
		for _, s := range d.Stops {
			name := strings.TrimSpace(s.Name)
			if s.POIID == "" || name == "" || asked[s.POIID] {
				continue
			}
			if v, ok := held[s.POIID]; ok && (!v.Matched || v.Fresh(now)) {
				continue
			}
			asked[s.POIID] = true
			due = append(due, POI{ID: s.POIID, Name: name, Category: s.Category})
		}
		if len(due) == 0 {
			continue
		}
		if ctx.Err() != nil || !allow() {
			return visits
		}
		visits++
		lookup, cancel := context.WithTimeout(ctx, lookupTimeout)
		_, err := p.svc.CheckPOIs(lookup, d.Centre, due)
		cancel()
		if err != nil {
			p.log.WarnContext(ctx, "placecheck: refresh lookup failed",
				slog.Int("places", len(due)), slog.Any("error", err))
		}
	}
	return visits
}
