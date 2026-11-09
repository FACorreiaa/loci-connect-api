package trip

import (
	"context"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// defaultAirportResults is what SearchAirports returns when limit is 0.
const defaultAirportResults = 8

// WithAirports attaches the airport dataset behind SearchAirports and
// ResolveFlightDestination. Nil is supported: both then answer empty, and
// the flight form falls back to names (Google Flights only).
func (h *Handler) WithAirports(a *flights.Airports) *Handler {
	h.airports = a
	return h
}

func (h *Handler) SearchAirports(ctx context.Context, req *connect.Request[tripv1.SearchAirportsRequest]) (*connect.Response[tripv1.SearchAirportsResponse], error) {
	if _, err := userID(ctx); err != nil {
		return nil, err
	}
	res := &tripv1.SearchAirportsResponse{}
	if h.airports == nil {
		return connect.NewResponse(res), nil
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = defaultAirportResults
	}
	for _, o := range h.airports.Search(req.Msg.GetQuery(), limit) {
		res.Options = append(res.Options, placeOptionToProto(o))
	}
	return connect.NewResponse(res), nil
}

func (h *Handler) ResolveFlightDestination(ctx context.Context, req *connect.Request[tripv1.ResolveFlightDestinationRequest]) (*connect.Response[tripv1.ResolveFlightDestinationResponse], error) {
	if _, err := userID(ctx); err != nil {
		return nil, err
	}
	res := &tripv1.ResolveFlightDestinationResponse{}
	if h.airports == nil {
		return connect.NewResponse(res), nil
	}
	if o, ok := h.airports.Resolve(req.Msg.GetCity(), req.Msg.GetCountryCode(), req.Msg.Latitude, req.Msg.Longitude); ok {
		res.Destination = placeOptionToProto(o)
	}
	return connect.NewResponse(res), nil
}
