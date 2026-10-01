package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// TripActions applies and dismisses what the agent proposed (tripaction.Service).
type TripActions interface {
	Apply(ctx context.Context, userID, proposalID uuid.UUID, option *int, baseVersion int64) (*trip.Trip, *locitypes.ConversationMessage, error)
	Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error
}

// WithTripActions attaches ApplyTripAction / DismissTripAction. Without it
// they answer Unimplemented.
func (h *ChatHandler) WithTripActions(a TripActions) *ChatHandler {
	h.tripActions = a
	return h
}

var errTripActionsOff = errors.New("trip actions are not enabled")

func (h *ChatHandler) tripActionCaller(ctx context.Context, proposalID string) (uuid.UUID, uuid.UUID, error) {
	if h.tripActions == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnimplemented, errTripActionsOff)
	}
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	id, err := uuid.Parse(proposalID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid proposal ID"))
	}
	return userID, id, nil
}

func (h *ChatHandler) ApplyTripAction(ctx context.Context, req *connect.Request[chatv1.ApplyTripActionRequest]) (*connect.Response[chatv1.ApplyTripActionResponse], error) {
	userID, id, err := h.tripActionCaller(ctx, req.Msg.GetProposalId())
	if err != nil {
		return nil, err
	}
	var option *int
	if req.Msg.OptionIndex != nil {
		v := int(req.Msg.GetOptionIndex())
		option = &v
	}
	t, msg, err := h.tripActions.Apply(ctx, userID, id, option, req.Msg.GetBaseVersion())
	if err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	res := &chatv1.ApplyTripActionResponse{Trip: trip.ToProto(t)}
	if msg != nil {
		res.Confirmation = presenter.ToConversationMessage(*msg)
	}
	return connect.NewResponse(res), nil
}

func (h *ChatHandler) DismissTripAction(ctx context.Context, req *connect.Request[chatv1.DismissTripActionRequest]) (*connect.Response[chatv1.DismissTripActionResponse], error) {
	userID, id, err := h.tripActionCaller(ctx, req.Msg.GetProposalId())
	if err != nil {
		return nil, err
	}
	if err := h.tripActions.Dismiss(ctx, userID, id); err != nil {
		return nil, h.tripActionError(ctx, err)
	}
	return connect.NewResponse(&chatv1.DismissTripActionResponse{}), nil
}

func (h *ChatHandler) tripActionError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, tripaction.ErrNotFound), errors.Is(err, trip.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, tripaction.ErrNotPending), errors.Is(err, tripaction.ErrExpired), errors.Is(err, trip.ErrVersionConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, tripaction.ErrNoOption), errors.Is(err, trip.ErrInvalidEdit):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "trip action failed", "error", err)
		}
		return connect.NewError(connect.CodeInternal, errors.New("the change could not be applied"))
	}
}
