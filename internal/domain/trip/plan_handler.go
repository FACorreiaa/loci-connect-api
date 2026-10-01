package trip

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/flights"
)

// WithPlan attaches the trip plan (dates, stays, flights). Without it those
// RPCs answer Unimplemented.
func (h *Handler) WithPlan(svc *Service) *Handler {
	h.plan = svc
	return h
}

var errPlanDisabled = errors.New("trip plans are not enabled")

// planTarget resolves the caller and the trip a plan RPC edits.
func (h *Handler) planTarget(ctx context.Context, tripID string) (uuid.UUID, uuid.UUID, error) {
	if h.plan == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnimplemented, errPlanDisabled)
	}
	uid, err := userID(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	id, err := uuid.Parse(tripID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	return uid, id, nil
}

func (h *Handler) planResult(ctx context.Context, t *Trip, err error) (*connect.Response[tripv1.TripDraft], error) {
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(h.respond(ctx, t)), nil
}

func (h *Handler) SetTripDates(ctx context.Context, req *connect.Request[tripv1.SetTripDatesRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	start, err := parseDate(req.Msg.GetStartDate())
	if err != nil {
		return nil, toConnectErr(err)
	}
	end, err := parseDate(req.Msg.GetEndDate())
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.plan.SetDates(ctx, uid, id, req.Msg.GetBaseVersion(), start, end)
	return h.planResult(ctx, t, err)
}

func (h *Handler) SetStay(ctx context.Context, req *connect.Request[tripv1.SetStayRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	stay, err := stayFromProto(req.Msg.GetStay())
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.plan.SetStay(ctx, uid, id, req.Msg.GetBaseVersion(), stay)
	return h.planResult(ctx, t, err)
}

func (h *Handler) ClearStay(ctx context.Context, req *connect.Request[tripv1.ClearStayRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	t, err := h.plan.ClearStay(ctx, uid, id, req.Msg.GetBaseVersion(), req.Msg.GetCityName())
	return h.planResult(ctx, t, err)
}

func (h *Handler) AddFlight(ctx context.Context, req *connect.Request[tripv1.AddFlightRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	f, err := flightFromProto(req.Msg.GetFlight())
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.plan.AddFlight(ctx, uid, id, req.Msg.GetBaseVersion(), f)
	return h.planResult(ctx, t, err)
}

func (h *Handler) RemoveFlight(ctx context.Context, req *connect.Request[tripv1.RemoveFlightRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, id, err := h.planTarget(ctx, req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	flightID, err := uuid.Parse(req.Msg.GetFlightId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid flight ID"))
	}
	t, err := h.plan.RemoveFlight(ctx, uid, id, req.Msg.GetBaseVersion(), flightID)
	return h.planResult(ctx, t, err)
}

func (h *Handler) BuildFlightLinks(ctx context.Context, req *connect.Request[tripv1.BuildFlightLinksRequest]) (*connect.Response[tripv1.BuildFlightLinksResponse], error) {
	if h.plan == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errPlanDisabled)
	}
	if _, err := userID(ctx); err != nil {
		return nil, err
	}
	depart, err := parseDate(req.Msg.GetDepartDate())
	if err != nil {
		return nil, toConnectErr(err)
	}
	ret, err := parseOptionalDate(req.Msg.ReturnDate)
	if err != nil {
		return nil, toConnectErr(err)
	}
	links := h.plan.FlightLinks(flights.Query{
		Origin: placeFromProto(req.Msg.GetOrigin()), Destination: placeFromProto(req.Msg.GetDestination()),
		Depart: depart, Return: ret,
		Passengers: int(req.Msg.GetPassengers()), Cabin: flights.Cabin(req.Msg.GetCabin()),
	})
	res := &tripv1.BuildFlightLinksResponse{}
	for _, l := range links {
		res.Links = append(res.Links, &tripv1.FlightLink{Provider: l.Provider, Label: l.Label, Url: l.URL})
	}
	return connect.NewResponse(res), nil
}
