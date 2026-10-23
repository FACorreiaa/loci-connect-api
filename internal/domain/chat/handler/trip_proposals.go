package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// TripProposals is planning together's side of proposals: a trip's members
// suggest stop edits, list them and react (tripaction.Service). Applying and
// dismissing stay on ApplyTripAction / DismissTripAction.
type TripProposals interface {
	ProposeEdit(ctx context.Context, userID, tripID uuid.UUID, a tripaction.Action, baseVersion int64) (*tripaction.View, error)
	List(ctx context.Context, userID, tripID uuid.UUID, includeClosed bool) ([]tripaction.View, error)
	React(ctx context.Context, userID, proposalID uuid.UUID, value int) (*tripaction.View, error)
}

// WithTripProposals attaches ProposeTripChange / ListTripProposals /
// ReactToTripProposal. Without it they answer Unimplemented.
func (h *ChatHandler) WithTripProposals(p TripProposals) *ChatHandler {
	h.proposals = p
	return h
}

// proposalCaller is the signed-in caller and the id they named.
func (h *ChatHandler) proposalCaller(ctx context.Context, rawID, what string) (uuid.UUID, uuid.UUID, error) {
	if h.proposals == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnimplemented, tripaction.ErrTogetherOff)
	}
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid "+what+" ID"))
	}
	return userID, id, nil
}

func (h *ChatHandler) ProposeTripChange(ctx context.Context, req *connect.Request[chatv1.ProposeTripChangeRequest]) (*connect.Response[chatv1.ActionProposal], error) {
	userID, tripID, err := h.proposalCaller(ctx, req.Msg.GetTripId(), "trip")
	if err != nil {
		return nil, err
	}
	a, err := tripaction.ActionFromProto(req.Msg.GetAction())
	if err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	v, err := h.proposals.ProposeEdit(ctx, userID, tripID, a, req.Msg.GetBaseVersion())
	if err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	return connect.NewResponse(tripaction.ViewToProto(v)), nil
}

func (h *ChatHandler) ListTripProposals(ctx context.Context, req *connect.Request[chatv1.ListTripProposalsRequest]) (*connect.Response[chatv1.ListTripProposalsResponse], error) {
	userID, tripID, err := h.proposalCaller(ctx, req.Msg.GetTripId(), "trip")
	if err != nil {
		return nil, err
	}
	views, err := h.proposals.List(ctx, userID, tripID, req.Msg.GetIncludeClosed())
	if err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	res := &chatv1.ListTripProposalsResponse{Proposals: make([]*chatv1.ActionProposal, 0, len(views))}
	for i := range views {
		res.Proposals = append(res.Proposals, tripaction.ViewToProto(&views[i]))
	}
	return connect.NewResponse(res), nil
}

func (h *ChatHandler) ReactToTripProposal(ctx context.Context, req *connect.Request[chatv1.ReactToTripProposalRequest]) (*connect.Response[chatv1.ActionProposal], error) {
	userID, id, err := h.proposalCaller(ctx, req.Msg.GetProposalId(), "proposal")
	if err != nil {
		return nil, err
	}
	v, err := h.proposals.React(ctx, userID, id, int(req.Msg.GetValue()))
	if err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	return connect.NewResponse(tripaction.ViewToProto(v)), nil
}
