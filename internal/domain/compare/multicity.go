package compare

import (
	"github.com/FACorreiaa/loci-connect-api/internal/domain/multicity"
	comparev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
)

// planRoute turns the resolved comparison columns into a multi-city route.
//
// Everything the planner needs was already gathered to build the columns —
// coordinates, go-scores, POI counts — so planning costs no extra queries.
func (s *Service) planRoute(
	originName string,
	originLat, originLon float64,
	resolved []resolvedCity,
	in CompareInput,
) multicity.Route {
	candidates := make([]multicity.City, 0, len(resolved))
	for _, r := range resolved {
		candidates = append(candidates, multicity.City{
			ID:       r.id,
			Name:     r.name,
			Lat:      r.lat,
			Lon:      r.lon,
			Score:    r.goScore,
			POICount: r.poiCount,
		})
	}

	return multicity.Plan(multicity.Input{
		OriginName: originName,
		OriginLat:  originLat,
		OriginLon:  originLon,
		Candidates: candidates,
		Start:      in.Start,
		End:        in.End,
		// Comparing candidates from a home city implies going home again, and
		// leaving the return out would make every route look cheaper than it is.
		ReturnToOrigin: true,
	})
}

// toMultiCityPlanProto converts a planned route to the wire shape, carrying each
// city's go-score across so the route can be justified rather than asserted.
func toMultiCityPlanProto(
	route multicity.Route,
	resolved []resolvedCity,
	allowed bool,
) *comparev1.MultiCityPlan {
	scoreByCity := make(map[string]*resolvedCity, len(resolved))
	for i := range resolved {
		scoreByCity[resolved[i].name] = &resolved[i]
	}

	// Group day numbers by city so the client does not have to.
	daysByCity := map[string][]int32{}
	for _, d := range route.Days {
		daysByCity[d.CityName] = append(daysByCity[d.CityName], int32(d.DayNumber))
	}

	out := &comparev1.MultiCityPlan{
		Feasible:        route.Feasible,
		TotalTravelMins: int32(route.TotalTravelMins),
		TravelShare:     clampShare(route.TravelShare),
		Outline:         route.Outline,
		Warnings:        route.Warnings,
		ProOnly:         !allowed,
	}

	for _, c := range route.Cities {
		city := &comparev1.PlannedCity{
			CityName:   c.Name,
			Lat:        c.Lat,
			Lon:        c.Lon,
			DayNumbers: daysByCity[c.Name],
		}
		if c.ID != "" {
			id := c.ID
			city.CityId = &id
		}
		if r, ok := scoreByCity[c.Name]; ok {
			city.GoScore = r.scorePB
		}
		out.Cities = append(out.Cities, city)
	}

	for _, d := range route.Dropped {
		out.Dropped = append(out.Dropped, &comparev1.DroppedCity{
			CityName: d.CityName,
			Reason:   d.Reason,
		})
	}

	out.Legs = toTripLegProtos(route.Legs)
	return out
}

// toTripLegProtos converts planner legs to the trip wire type, which is the same
// message a saved multi-city trip stores — so "save this route" is a copy rather
// than a translation.
func toTripLegProtos(legs []multicity.Leg) []*tripv1.TripLeg {
	out := make([]*tripv1.TripLeg, 0, len(legs))
	for _, l := range legs {
		out = append(out, &tripv1.TripLeg{
			FromName:     l.FromName,
			ToName:       l.ToName,
			FromLat:      l.FromLat,
			FromLon:      l.FromLon,
			ToLat:        l.ToLat,
			ToLon:        l.ToLon,
			DistanceKm:   l.DistanceKm,
			DurationMins: int32(l.DurationMins),
			AfterDay:     int32(l.AfterDay),
			Mode:         l.Mode,
		})
	}
	return out
}

// clampShare keeps travel_share inside the 0-1 the proto declares, even if a
// pathological window pushes the raw ratio past it.
func clampShare(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
