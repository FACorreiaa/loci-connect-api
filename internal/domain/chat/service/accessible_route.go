package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/geo"
	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
)

const (
	// routeBudget bounds all of a turn's routing. It runs after the itinerary
	// event, so it delays only the completion event, never first paint.
	routeBudget = 10 * time.Second
	// routeConcurrency caps parallel router calls per answer: the hosted
	// router allows 40 a minute.
	routeConcurrency = 4

	// Straight-line estimates: streets are longer than the crow flies, and a
	// wheelchair is a little slower than walking on average.
	detourFactor        = 1.3
	walkingSpeedKmH     = 4.5
	wheelchairSpeedKmH  = 4.0
	slowPaceFactor      = 1.5
	notableAscentMetres = 25
	maxRouterWarnings   = 3
)

// routeOptionsFor maps needs onto router options. Anyone on wheels, pushing a
// stroller, or asking for step-free routes gets the wheelchair profile, which
// is the only one that can avoid steps, steep slopes and high kerbs.
func routeOptionsFor(needs *locitypes.AccessibilityNeeds) routing.Options {
	if !needs.NeedsStepFree() {
		return routing.Options{Profile: routing.ProfileWalking}
	}
	opts := routing.Options{
		Profile:       routing.ProfileWheelchair,
		AvoidSteps:    true,
		MaxInclinePct: needs.MaxIncline.Percent(),
		PavedOnly:     needs.AvoidCobbles,
	}
	// Manual chairs struggle with kerbs a powered chair takes in its stride.
	if needs.Mobility == locitypes.MobilityManualWheelchair {
		opts.MaxSlopedKerbM = 0.03
	}
	return opts
}

// paceFactor stretches router durations, which assume an unhurried able-bodied
// pace, for travellers who move more slowly.
func paceFactor(needs *locitypes.AccessibilityNeeds) float64 {
	switch {
	case needs.Mobility == locitypes.MobilityLimitedWalking,
		needs.Mobility == locitypes.MobilityWalkingAid,
		needs.FatigueOrChronicPain:
		return slowPaceFactor
	default:
		return 1
	}
}

// emitAccessibleRoutes sends one ACCESSIBLE_ROUTE event per planned day.
func (l *ServiceImpl) emitAccessibleRoutes(ctx context.Context, cc *common.ChatContext, pois []locitypes.POIDetailedInfo) {
	if !cc.AccessibilityNeeds.Active() {
		return
	}
	for _, day := range l.buildAccessibleRoutes(ctx, cc.AccessibilityNeeds, pois) {
		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
			Type:      locitypes.EventTypeAccessibleRoute,
			Data:      day,
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}, 3)
	}
}

type legJob struct {
	day      int
	index    int
	from, to locitypes.POIDetailedInfo
}

// buildAccessibleRoutes computes each day's legs between consecutive stops,
// in the order the itinerary lists them. Places without a plan day or without
// coordinates are skipped: there is nothing honest to say about them.
func (l *ServiceImpl) buildAccessibleRoutes(ctx context.Context, needs *locitypes.AccessibilityNeeds, pois []locitypes.POIDetailedInfo) []locitypes.StreamAccessibleRouteData {
	if !needs.Active() {
		return nil
	}
	byDay := map[int][]locitypes.POIDetailedInfo{}
	for _, p := range pois {
		if p.Day < 1 || (p.Latitude == 0 && p.Longitude == 0) {
			continue
		}
		byDay[p.Day] = append(byDay[p.Day], p)
	}
	days := make([]int, 0, len(byDay))
	var jobs []legJob
	for d, stops := range byDay {
		days = append(days, d)
		for i := 1; i < len(stops); i++ {
			jobs = append(jobs, legJob{day: d, index: i - 1, from: stops[i-1], to: stops[i]})
		}
	}
	sort.Ints(days)
	if len(jobs) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, routeBudget)
	defer cancel()
	opts := routeOptionsFor(needs)

	legs := make(map[int][]locitypes.WalkingLeg, len(days))
	for d, stops := range byDay {
		if len(stops) > 1 {
			legs[d] = make([]locitypes.WalkingLeg, len(stops)-1)
		}
	}
	sem := make(chan struct{}, routeConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leg := l.walkingLeg(ctx, sem, needs, opts, j.from, j.to)
			mu.Lock()
			legs[j.day][j.index] = leg
			mu.Unlock()
		}()
	}
	wg.Wait()

	out := make([]locitypes.StreamAccessibleRouteData, 0, len(days))
	for _, d := range days {
		dayLegs := legs[d]
		if len(dayLegs) == 0 {
			continue
		}
		total := 0
		for _, leg := range dayLegs {
			total += leg.DurationMin
		}
		data := locitypes.StreamAccessibleRouteData{Day: d, Legs: dayLegs, TotalWalkMinutes: total}
		if limit := needs.MaxWalkMinutesPerDay; limit > 0 && total > limit {
			data.Warnings = append(data.Warnings, fmt.Sprintf(
				"Day %d has about %d minutes of walking between stops, over your %d-minute daily limit. Consider dropping a stop or taking taxis for the longer legs.",
				d, total, limit))
		}
		out = append(out, data)
	}
	return out
}

// walkingLeg computes one leg, falling back to an honestly labelled estimate.
func (l *ServiceImpl) walkingLeg(ctx context.Context, sem chan struct{}, needs *locitypes.AccessibilityNeeds, opts routing.Options, from, to locitypes.POIDetailedInfo) locitypes.WalkingLeg {
	leg := locitypes.WalkingLeg{FromPOIID: poiRef(from), ToPOIID: poiRef(to)}
	fromLL := routing.LatLon{Lat: from.Latitude, Lon: from.Longitude}
	toLL := routing.LatLon{Lat: to.Latitude, Lon: to.Longitude}

	var routed routing.Leg
	err := routing.ErrUnavailable
	if l.pedestrianRouter != nil {
		select {
		case sem <- struct{}{}:
			routed, err = l.pedestrianRouter.Route(ctx, fromLL, toLL, opts)
			<-sem
		case <-ctx.Done():
		}
	}

	switch {
	case err == nil:
		leg.Routed = true
		leg.DistanceM = int(math.Round(routed.DistanceM))
		leg.DurationMin = minutes(routed.DurationS / 60 * paceFactor(needs))
		leg.AscentM = int(math.Round(routed.AscentM))
		if leg.AscentM >= notableAscentMetres {
			leg.Notes = append(leg.Notes, routedNote(fmt.Sprintf("Climbs about %d m on the way", leg.AscentM), locitypes.AccessibilitySeverityCaution))
		}
		for i, w := range routed.Warnings {
			if i == maxRouterWarnings {
				break
			}
			leg.Notes = append(leg.Notes, routedNote(w, locitypes.AccessibilitySeverityCaution))
		}

	case errors.Is(err, routing.ErrNoRoute):
		// The router looked and found none: for a wheelchair, typically "only
		// via steps". That is the most important thing on the leg.
		estimateLeg(&leg, needs, fromLL, toLL)
		text := "No step-free route found between these stops. A taxi is the safer option."
		if !needs.NeedsStepFree() {
			text = "No walking route found between these stops. A taxi is the safer option."
		}
		leg.Notes = append(leg.Notes, routedNote(text, locitypes.AccessibilitySeverityBarrier))
		leg.SuggestMode = "taxi"

	default:
		if l.pedestrianRouter != nil && ctx.Err() == nil {
			l.logger.DebugContext(ctx, "walking leg estimated: router unavailable", slog.Any("error", err))
		}
		estimateLeg(&leg, needs, fromLL, toLL)
		severity := locitypes.AccessibilitySeverityInfo
		if needs.NeedsStepFree() {
			severity = locitypes.AccessibilitySeverityCaution
		}
		leg.Notes = append(leg.Notes, locitypes.AccessibilityNote{
			Text:     "Straight-line estimate: the route was not checked for steps or slopes.",
			Severity: severity,
			Source:   locitypes.AccessibilitySourceAIEstimate,
		})
	}

	if limit := needs.MaxWalkMinutesPerLeg; limit > 0 && leg.DurationMin > limit {
		leg.Notes = append(leg.Notes, routedNote(fmt.Sprintf(
			"About %d minutes, over your %d-minute limit. A taxi may be easier.", leg.DurationMin, limit),
			locitypes.AccessibilitySeverityCaution))
		leg.SuggestMode = "taxi"
	}
	if rest := needs.RestEveryMinutes; rest > 0 && leg.DurationMin > rest {
		leg.Notes = append(leg.Notes, routedNote(fmt.Sprintf(
			"Longer than your %d-minute rest interval: plan a seated break on the way.", rest),
			locitypes.AccessibilitySeverityInfo))
	}
	if len(leg.Notes) > locitypes.MaxAccessibilityNotes {
		leg.Notes = leg.Notes[:locitypes.MaxAccessibilityNotes]
	}
	return leg
}

// estimateLeg fills distance and duration from a straight line, stretched for
// streets and slowed for the traveller.
func estimateLeg(leg *locitypes.WalkingLeg, needs *locitypes.AccessibilityNeeds, from, to routing.LatLon) {
	km := geo.HaversineKm(from.Lat, from.Lon, to.Lat, to.Lon) * detourFactor
	speed := walkingSpeedKmH
	if needs.Mobility.UsesWheels() {
		speed = wheelchairSpeedKmH
	}
	leg.Routed = false
	leg.DistanceM = int(math.Round(km * 1000))
	leg.DurationMin = minutes(km / speed * 60 * paceFactor(needs))
}

func minutes(m float64) int {
	if m <= 0 {
		return 0
	}
	return max(1, int(math.Round(m)))
}

func routedNote(text string, sev locitypes.AccessibilityNoteSeverity) locitypes.AccessibilityNote {
	return locitypes.AccessibilityNote{Text: text, Severity: sev, Source: locitypes.AccessibilitySourceRouted}
}

func poiRef(p locitypes.POIDetailedInfo) string {
	if p.ID != uuid.Nil {
		return p.ID.String()
	}
	return p.Name
}
