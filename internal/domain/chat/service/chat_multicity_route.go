package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/multicity"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// maxTripCities is the most cities one trip plans. Each costs a full
// generation, one after another.
const maxTripCities = 5

// minDaysPerCity is the least a city gets when the traveller did not say how
// long the trip is. The default horizon is a short sample; three cities in it
// would leave the planner dropping two of them.
const minDaysPerCity = 2

// ErrNoCitiesResolved means a multi-city request named cities, none of which
// could be found.
var ErrNoCitiesResolved = errors.New("none of these cities could be found")

// CityResolver is the geocoder the compare service already uses.
type CityResolver interface {
	Resolve(ctx context.Context, q city.ResolveQuery) (*city.Resolved, error)
}

// multiCityStop is one city of a planned multi-city trip.
type multiCityStop struct {
	Index    int
	CityName string
	CityID   uuid.UUID
	Lat, Lon float64
	// Days are trip-wide day numbers spent here.
	Days   []int
	Nights int
	// SessionID is the child chat session this city is generated in.
	SessionID uuid.UUID
}

// multiCityRoute is a planned multi-city trip.
type multiCityRoute struct {
	Stops   []multiCityStop
	Route   multicity.Route
	Dropped []locitypes.StreamDroppedStop
	// Message is the request with the cities taken out: what each city's
	// generation is asked, so its cache key matches a single-city request.
	Message string
}

// IsMulti reports whether the route really spans several cities. A route of
// one is a request that named several but kept one: it is planned as that
// single city, and Dropped says why the others went.
func (r *multiCityRoute) IsMulti() bool { return r != nil && len(r.Stops) >= 2 }

// routeRequest is what the traveller asked for, however they asked it.
type routeRequest struct {
	Cities       []ExtractedCity
	Ordered      bool
	SuggestOrder bool
	TotalDays    int
	DaysStated   bool
	Origin       *locitypes.UserLocation
	Message      string
}

// buildMultiCityRoute resolves the cities and lays out the trip. With fewer
// than two real cities it returns a route of one — the survivor, planned as a
// single-city trip — and a nil route when the geocoder itself failed, so the
// request takes today's single-city path unchanged.
func buildMultiCityRoute(ctx context.Context, r CityResolver, req routeRequest) (*multiCityRoute, error) {
	out := &multiCityRoute{Message: req.Message}

	var resolved []multicity.City
	nights := map[string]int{}
	lookupFailed := false
	for _, c := range req.Cities {
		if len(resolved) == maxTripCities {
			out.Dropped = append(out.Dropped, locitypes.StreamDroppedStop{
				CityName: c.Name, Reason: fmt.Sprintf("a trip plans at most %d cities", maxTripCities),
			})
			continue
		}
		q := city.ResolveQuery{Name: c.Name}
		if req.Origin != nil {
			q.NearLat, q.NearLon = req.Origin.UserLat, req.Origin.UserLon
		}
		got, err := r.Resolve(ctx, q)
		if err != nil && !errors.Is(err, city.ErrCityUnresolvable) {
			// Our geocoder failing says nothing about the city.
			lookupFailed = true
			out.Dropped = append(out.Dropped, locitypes.StreamDroppedStop{CityName: c.Name, Reason: "we couldn't look this city up just now"})
			continue
		}
		if err != nil || got == nil {
			out.Dropped = append(out.Dropped, locitypes.StreamDroppedStop{CityName: c.Name, Reason: "we couldn't find this city"})
			continue
		}
		name := got.City.Name
		if name == "" {
			name = c.Name
		}
		if _, dup := nights[name]; dup {
			// Two spellings of one city: keep the first, add the days.
			nights[name] += c.Days
			continue
		}
		mc := multicity.City{Name: name, Lat: got.Lat, Lon: got.Lon}
		if got.City.ID != uuid.Nil {
			mc.ID = got.City.ID.String()
		}
		resolved = append(resolved, mc)
		nights[name] = c.Days
	}

	switch len(resolved) {
	case 0:
		if lookupFailed {
			return nil, nil
		}
		return nil, ErrNoCitiesResolved
	case 1:
		return out.single(resolved[0]), nil
	}

	allNights := true
	for _, c := range resolved {
		if nights[c.Name] <= 0 {
			allNights = false
		}
	}

	in := multicity.Input{MultiModal: true, MaxCities: maxTripCities}
	var route multicity.Route
	if req.Ordered && allNights && !req.SuggestOrder {
		stops := make([]multicity.FixedStop, len(resolved))
		for i, c := range resolved {
			stops[i] = multicity.FixedStop{City: c, Nights: nights[c.Name]}
		}
		route = multicity.PlanFixed(in, stops)
	} else {
		days := req.TotalDays
		if allNights {
			days = 0
			for _, c := range resolved {
				days += nights[c.Name]
			}
		}
		if floor := minDaysPerCity * len(resolved); !req.DaysStated && days < floor {
			days = floor
		}
		start := time.Unix(0, 0).UTC()
		in.Start, in.End = start, start.Add(time.Duration(days)*24*time.Hour)
		in.Candidates = resolved
		route = multicity.Plan(in)
		for _, d := range route.Dropped {
			out.Dropped = append(out.Dropped, locitypes.StreamDroppedStop{CityName: d.CityName, Reason: d.Reason})
		}
	}

	if len(route.Cities) == 1 {
		// The planner kept one city: a single-city trip to it.
		return out.single(route.Cities[0]), nil
	}
	if len(route.Cities) == 0 {
		return out.single(resolved[0]), nil
	}

	out.Route = route
	for i, c := range route.Cities {
		s := multiCityStop{Index: i, CityName: c.Name, Lat: c.Lat, Lon: c.Lon}
		if id, err := uuid.Parse(c.ID); err == nil {
			s.CityID = id
		}
		for _, d := range route.Days {
			if d.CityName == c.Name {
				s.Days = append(s.Days, d.DayNumber)
			}
		}
		s.Nights = len(s.Days)
		out.Stops = append(out.Stops, s)
	}
	return out, nil
}

// single makes this a route of one city: the request is planned as that city.
func (r *multiCityRoute) single(c multicity.City) *multiCityRoute {
	s := multiCityStop{CityName: c.Name, Lat: c.Lat, Lon: c.Lon}
	if id, err := uuid.Parse(c.ID); err == nil {
		s.CityID = id
	}
	r.Stops = []multiCityStop{s}
	return r
}
