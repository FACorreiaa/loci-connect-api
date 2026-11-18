package trip

import (
	"context"
	"errors"
	"math"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

// StaySurroundings is what the map shows around a stay (*transit.Overpass).
type StaySurroundings interface {
	Around(ctx context.Context, p transit.LatLon) (transit.Surroundings, error)
}

const (
	// A walk is routed for the nearest few stops and for days that start
	// within walking distance; the rest are estimates, and say so. Routing is
	// metered, and a trip page should not spend the day's budget.
	maxRoutedStops = 4
	maxRoutedDays  = 7
	maxShownStops  = 8
	// An airport further than this is not the city's airport in any sense a
	// traveller would recognise.
	maxAirportKm = 120
)

// WithStayTransport attaches what GetStayTransport reads: the map around a
// stay, and a router for the walks. Both may be nil. Without the map the
// answer has no stops and says the map data could not be reached; without
// the router every walk is a straight-line estimate.
func (h *Handler) WithStayTransport(around StaySurroundings, router routing.PedestrianRouter) *Handler {
	h.around, h.walks = around, router
	return h
}

// GetStayTransport says how to get around from the stay in one city.
func (h *Handler) GetStayTransport(ctx context.Context, req *connect.Request[tripv1.GetStayTransportRequest]) (*connect.Response[tripv1.GetStayTransportResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	t, err := h.repo.GetTrip(ctx, id, uid)
	if errors.Is(err, ErrNotFound) && h.membersReady() == nil {
		t, _, err = h.onTrip(ctx, uid, id)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, toConnectErr(err)
	}

	var stay *TripStay
	for i := range t.Stays {
		if sameCity(t.Stays[i].CityName, req.Msg.GetCityName()) {
			stay = &t.Stays[i]
			break
		}
	}
	if stay == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no stay is set for that city"))
	}

	draft := tripToProto(t)
	h.hydrateStops(ctx, draft)
	out := stayTransport(ctx, draft, *stay, h.around, h.walks, h.airports)
	return connect.NewResponse(&tripv1.GetStayTransportResponse{Transport: out}), nil
}

// stayTransport builds the answer for one stay from the trip as a client
// sees it (stops carrying their places, so each has a position).
func stayTransport(
	ctx context.Context, draft *tripv1.TripDraft, stay TripStay,
	around StaySurroundings, router routing.PedestrianRouter, airports *flights.Airports,
) *tripv1.StayTransport {
	out := &tripv1.StayTransport{CityName: stay.CityName, StayName: stay.Name}
	if !stay.HasPin() {
		out.Status = tripv1.StayTransportStatus_STAY_TRANSPORT_STATUS_NO_COORDINATES
		return out
	}
	from := transit.LatLon{Lat: *stay.Lat, Lon: *stay.Lon}
	out.Status = tripv1.StayTransportStatus_STAY_TRANSPORT_STATUS_UNAVAILABLE

	if around != nil {
		if s, err := around.Around(ctx, from); err == nil {
			out.Status = tripv1.StayTransportStatus_STAY_TRANSPORT_STATUS_READY
			out.Attribution = transit.Attribution
			if !s.FetchedAt.IsZero() {
				out.FetchedAt = timestamppb.New(s.FetchedAt)
			}
			for i, st := range s.Stops {
				if i == maxShownStops {
					break
				}
				ts := &tripv1.TransitStop{
					Name: st.Name, Mode: transitModes[st.Mode], Lines: st.Lines,
					Latitude: st.Lat, Longitude: st.Lon,
					DistanceM: int32(st.DistanceM), WalkMinutes: int32(transit.EstimateWalkMinutes(st.DistanceM)),
				}
				if i < maxRoutedStops {
					if m, d, ok := routedWalk(ctx, router, from, transit.LatLon{Lat: st.Lat, Lon: st.Lon}); ok {
						ts.WalkMinutes, ts.DistanceM, ts.Routed = m, d, true
					}
				}
				out.Stops = append(out.Stops, ts)
			}
		}
	}

	routed := 0
	for _, d := range draft.GetDays() {
		city := d.GetCityName()
		if city == "" {
			city = draft.GetCityName()
		}
		if !sameCity(city, stay.CityName) {
			continue
		}
		first, to, ok := firstPlacedStop(d)
		if !ok {
			continue
		}
		dist := transit.DistanceM(from, to)
		ds := &tripv1.DayStart{
			DayId: d.GetId(), DayNumber: d.GetDayNumber(), FirstStopName: first.GetName(),
			DistanceM: int32(dist), WalkMinutes: int32(transit.EstimateWalkMinutes(dist)),
			SuggestedMode:       transit.SuggestedMode(dist),
			GoogleDirectionsUrl: transit.GoogleTransitURL(from, to),
			AppleDirectionsUrl:  transit.AppleTransitURL(from, to),
		}
		if dist <= transit.WalkableM && routed < maxRoutedDays {
			routed++
			if m, dm, ok := routedWalk(ctx, router, from, to); ok {
				ds.WalkMinutes, ds.DistanceM, ds.Routed = m, dm, true
				ds.SuggestedMode = transit.SuggestedMode(int(float64(dm) / 1.3))
			}
		}
		out.DayStarts = append(out.DayStarts, ds)
	}

	if airports != nil {
		if ap, ok := airports.Resolve(stay.CityName, "", stay.Lat, stay.Lon); ok && ap.Kind == flights.KindAirport && (ap.Lat != 0 || ap.Lon != 0) {
			to := transit.LatLon{Lat: ap.Lat, Lon: ap.Lon}
			if km := float64(transit.DistanceM(from, to)) / 1000; km <= maxAirportKm {
				out.Airport = &tripv1.AirportConnection{
					AirportName: ap.Name, Iata: ap.IATA, DistanceKm: math.Round(km*10) / 10,
					GoogleDirectionsUrl: transit.GoogleTransitURL(from, to),
					AppleDirectionsUrl:  transit.AppleTransitURL(from, to),
				}
			}
		}
	}
	return out
}

// firstPlacedStop is the day's first stop that has a position: its own pin,
// or the place behind it.
func firstPlacedStop(d *tripv1.TripDay) (*tripv1.TripStop, transit.LatLon, bool) {
	for _, s := range d.GetStops() {
		if s.Latitude != nil && s.Longitude != nil {
			return s, transit.LatLon{Lat: s.GetLatitude(), Lon: s.GetLongitude()}, true
		}
		if p := s.GetPoi(); p != nil && (p.GetLatitude() != 0 || p.GetLongitude() != 0) {
			return s, transit.LatLon{Lat: p.GetLatitude(), Lon: p.GetLongitude()}, true
		}
	}
	return nil, transit.LatLon{}, false
}

// routedWalk asks the router for a walk. Any failure (no router, budget
// spent, no route) is "not routed": the caller keeps its estimate.
func routedWalk(ctx context.Context, router routing.PedestrianRouter, from, to transit.LatLon) (minutes, metres int32, ok bool) {
	if router == nil {
		return 0, 0, false
	}
	leg, err := router.Route(ctx, routing.LatLon{Lat: from.Lat, Lon: from.Lon}, routing.LatLon{Lat: to.Lat, Lon: to.Lon},
		routing.Options{Profile: routing.ProfileWalking})
	if err != nil || leg.DurationS <= 0 {
		return 0, 0, false
	}
	return int32(max(1, math.Ceil(leg.DurationS/60))), int32(math.Round(leg.DistanceM)), true
}

var transitModes = map[transit.Mode]tripv1.TransitMode{
	transit.ModeMetro: tripv1.TransitMode_TRANSIT_MODE_METRO,
	transit.ModeTrain: tripv1.TransitMode_TRANSIT_MODE_TRAIN,
	transit.ModeTram:  tripv1.TransitMode_TRANSIT_MODE_TRAM,
	transit.ModeBus:   tripv1.TransitMode_TRANSIT_MODE_BUS,
	transit.ModeFerry: tripv1.TransitMode_TRANSIT_MODE_FERRY,
}
