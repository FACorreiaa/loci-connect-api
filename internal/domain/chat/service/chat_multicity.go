package service

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/tripspan"
)

// cityBudget bounds one city's generation. A multi-city stream runs several
// one after another, so the handler gives the stream as a whole one of these
// per possible city.
const cityBudget = 3 * time.Minute

var errNoCityPlanned = errors.New("we couldn't plan any of these cities — try again in a moment")

// planMultiCity decides whether this turn is a multi-city trip. It returns a
// nil route for a single-city turn. The extractor call it makes is cached on
// the message, so the single-city path that follows pays nothing for it.
func (l *ServiceImpl) planMultiCity(cc *common.ChatContext) (*multiCityRoute, error) {
	if l.cityResolver == nil {
		return nil, nil
	}
	span := tripspan.Parse(cc.Message)
	req := routeRequest{
		TotalDays:    span.Days,
		DaysStated:   span.Parsed(),
		Origin:       cc.UserLocation,
		SuggestOrder: cc.SuggestOrder,
		Message:      cc.Message,
	}
	if len(cc.Stops) >= 2 {
		req.Ordered = true
		for _, s := range cc.Stops {
			req.Cities = append(req.Cities, ExtractedCity{Name: s.CityName, Days: s.Nights})
		}
	} else {
		tc, err := l.extractTripCitiesCached(cc.Ctx, cc.Message)
		if err != nil || len(tc.Cities) < 2 {
			// Not knowing is not a reason to fail: the single-city path
			// extracts again (from cache) and reports its own errors.
			return nil, nil
		}
		req.Cities, req.Ordered, req.Message = tc.Cities, tc.Ordered, tc.Message
	}
	route, err := buildMultiCityRoute(cc.Ctx, l.cityResolver, req)
	if err != nil {
		return nil, err
	}
	if route == nil && len(cc.Stops) >= 2 && cc.CityName == "" {
		// The builder asked for several cities and only one survived: plan it
		// as that city rather than re-extracting from free text.
		cc.CityName = cc.Stops[0].CityName
	}
	return route, nil
}

// processMultiCity runs the single-city pipeline once per city, in order, on
// one stream: START and ROUTE first, then each city's events tagged with its
// index, then one parent trip and one COMPLETE.
func (l *ServiceImpl) processMultiCity(cc common.ChatContext, r *multiCityRoute) error {
	ctx := cc.Ctx
	for i := range r.Stops {
		if r.Stops[i].SessionID == uuid.Nil {
			r.Stops[i].SessionID = uuid.New()
		}
	}
	first := r.Stops[0]
	domain := (&locitypes.DomainDetector{}).DetectDomain(ctx, r.Message)

	// The first city's session names the run: the resume buffer, the run
	// tracker and the client's URL all key on the START's session id.
	l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{
		Type: locitypes.EventTypeStart,
		Data: locitypes.StreamStartData{SessionID: first.SessionID.String(), Domain: string(domain), City: first.CityName},
	}, 3)
	l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeRoute, Data: routeData(r, "")}, 3)

	perStop := make([]*trip.Trip, len(r.Stops))
	succeeded := 0
	for i, s := range r.Stops {
		child := cc
		child.CityName = s.CityName
		child.Message = r.Message
		child.StopRun = true
		child.PresetSessionID = s.SessionID
		child.PresetTripDays = len(s.Days)
		child.SuppressTripSave = true
		child.RequestedSessionID = uuid.Nil
		child.TripID = uuid.Nil
		child.SessionID = uuid.Nil
		child.Stops = nil

		stopCh := make(chan locitypes.StreamEvent, 100)
		done := make(chan struct{})
		go func(index int) {
			defer close(done)
			for ev := range stopCh {
				if out, keep := forwardStopEvent(ev, index); keep {
					l.sendEvent(ctx, cc.EventCh, out, 3)
				}
			}
		}(i)
		child.EventCh = stopCh

		started := time.Now()
		data, err := l.runCity(child)
		close(stopCh)
		<-done

		if err != nil {
			l.logger.WarnContext(ctx, "multi-city: a city failed; continuing with the rest",
				slog.Int("stop", i), slog.String("city", s.CityName), slog.Any("error", err))
			continue
		}
		succeeded++
		l.logger.InfoContext(ctx, "multi-city: city generated",
			slog.Int("stop", i), slog.String("city", s.CityName), slog.Duration("took", time.Since(started)))
		perStop[i] = stopTrip(child, s, data)
	}

	if succeeded == 0 {
		l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: errNoCityPlanned.Error()}, 3)
		return errNoCityPlanned
	}

	fin := cc
	fin.SessionID = first.SessionID
	fin.CityName = first.CityName
	fin.Domain = domain
	fin.TripID = uuid.Nil
	if l.tripRepo != nil {
		saved, err := l.tripRepo.SaveTrip(ctx, mergeStopTrips(cc.UserID, r, perStop), 0)
		if err != nil {
			l.logger.WarnContext(ctx, "multi-city: parent trip not saved", slog.Any("error", err))
		} else {
			fin.TripID = saved.ID
			l.sendEvent(ctx, cc.EventCh, locitypes.StreamEvent{Type: locitypes.EventTypeRoute, Data: routeData(r, saved.ID.String())}, 3)
		}
	}
	l.sendCompletionEvent(&fin)
	return nil
}

// stopTrip is one city's generated places as trip days, spread over that
// city's own days — the child context the pipeline ran with is a copy, so its
// day count has to be set again here.
func stopTrip(child common.ChatContext, s multiCityStop, data *locitypes.AiCityResponse) *trip.Trip {
	child.SessionID = s.SessionID
	child.CityName = s.CityName
	child.TripDays = len(s.Days)
	return buildTripFromCityResponse(&child, data, "")
}

// forwardStopEvent adapts one city's event for the multi-city stream. The
// city's own START becomes progress (the run already has one), its COMPLETE is
// dropped (the run completes once), and everything else is tagged with the
// city's index — which also makes its ERROR non-terminal.
func forwardStopEvent(ev locitypes.StreamEvent, index int) (locitypes.StreamEvent, bool) {
	idx := index
	ev.StopIndex = &idx
	switch ev.Type {
	case locitypes.EventTypeComplete:
		return ev, false
	case locitypes.EventTypeStart:
		ev.Type = locitypes.EventTypeProgress
		ev.Message = "city_started"
	}
	return ev, true
}

// routeData is the ROUTE payload for this route.
func routeData(r *multiCityRoute, tripID string) locitypes.StreamRouteData {
	out := locitypes.StreamRouteData{
		Outline:         r.Route.Outline,
		Warnings:        r.Route.Warnings,
		Dropped:         r.Dropped,
		TotalTravelMins: r.Route.TotalTravelMins,
		TripID:          tripID,
	}
	for _, s := range r.Stops {
		rs := locitypes.StreamRouteStop{Index: s.Index, CityName: s.CityName, SessionID: s.SessionID.String(), DayNumbers: s.Days}
		if s.CityID != uuid.Nil {
			rs.CityID = s.CityID.String()
		}
		out.Stops = append(out.Stops, rs)
	}
	for _, lg := range r.Route.Legs {
		out.Legs = append(out.Legs, locitypes.StreamRouteLeg{
			AfterDay: lg.AfterDay, FromName: lg.FromName, ToName: lg.ToName,
			FromLat: lg.FromLat, FromLon: lg.FromLon, ToLat: lg.ToLat, ToLon: lg.ToLon,
			DistanceKm: lg.DistanceKm, DurationMins: lg.DurationMins, Mode: lg.Mode,
		})
	}
	return out
}

// mergeStopTrips folds each city's generated days into one trip: days
// renumbered across the route, each tagged with its city, the arrival day in
// every city after the first marked as a travel day, plus the legs and the
// cities linked to their sessions. A city that failed keeps its (empty) days,
// so the trip still shows where the traveller is on those days.
func mergeStopTrips(userID uuid.UUID, r *multiCityRoute, perStop []*trip.Trip) *trip.Trip {
	names := make([]string, len(r.Stops))
	for i, s := range r.Stops {
		names[i] = s.CityName
	}
	source := r.Stops[0].SessionID.String()
	out := &trip.Trip{
		UserID:          userID,
		CityName:        r.Stops[0].CityName,
		Title:           strings.Join(names, " + "),
		SourceSessionID: &source,
	}
	if r.Stops[0].CityID != uuid.Nil {
		id := r.Stops[0].CityID
		out.CityID = &id
	}

	for i, s := range r.Stops {
		var local []trip.TripDay
		if i < len(perStop) && perStop[i] != nil {
			local = perStop[i].Days
		}
		for k, dayNum := range s.Days {
			lat, lon := s.Lat, s.Lon
			day := trip.TripDay{
				DayNumber: int32(dayNum),
				CityName:  s.CityName,
				CityLat:   &lat,
				CityLon:   &lon,
				TravelDay: k == 0 && i > 0,
			}
			if s.CityID != uuid.Nil {
				id := s.CityID
				day.CityID = &id
			}
			for _, ld := range local {
				if int(ld.DayNumber) == k+1 {
					day.Stops = ld.Stops
				}
			}
			out.Days = append(out.Days, day)
		}
		sid := s.SessionID
		c := trip.TripCity{CityName: s.CityName, SessionID: &sid, Nights: int32(s.Nights), OrderIndex: int32(i)}
		if s.CityID != uuid.Nil {
			id := s.CityID
			c.CityID = &id
		}
		out.Cities = append(out.Cities, c)
	}

	for _, lg := range r.Route.Legs {
		fromLat, fromLon, toLat, toLon := lg.FromLat, lg.FromLon, lg.ToLat, lg.ToLon
		out.Legs = append(out.Legs, trip.TripLeg{
			AfterDay: int32(lg.AfterDay), FromName: lg.FromName, ToName: lg.ToName,
			FromLat: &fromLat, FromLon: &fromLon, ToLat: &toLat, ToLon: &toLon,
			DistanceKm: lg.DistanceKm, DurationMins: int32(lg.DurationMins), Mode: lg.Mode,
		})
	}
	return out
}
