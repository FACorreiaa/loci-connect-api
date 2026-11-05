package assistant

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant/assistantconnect"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler serves AssistantService. With no Service it answers Unimplemented,
// which is what clients read as "not available here".
type Handler struct {
	assistantconnect.UnimplementedAssistantServiceHandler
	svc    *Service
	logger *slog.Logger
}

// NewHandler creates a Handler; svc nil means the feature is switched off.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, logger: logger}
}

func (h *Handler) AskAssistant(
	ctx context.Context,
	req *connect.Request[assistantv1.AskAssistantRequest],
	stream *connect.ServerStream[assistantv1.AssistantEvent],
) error {
	if h.svc == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("the assistant is not available"))
	}
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	userID, err := uuid.Parse(raw)
	if !ok || err != nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("sign in to ask the assistant"))
	}
	err = h.svc.Ask(ctx, userID, req.Msg, stream.Send)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidConversation):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	default:
		h.logger.Error("assistant: ask failed", slog.Any("error", err))
		return connect.NewError(connect.CodeInternal, errors.New("the assistant could not answer"))
	}
}

// ConfirmAction carries out a card. The caller comes from the auth context, so
// nothing the model said can stand in for them.
func (h *Handler) ConfirmAction(
	ctx context.Context,
	req *connect.Request[assistantv1.ConfirmActionRequest],
) (*connect.Response[assistantv1.ConfirmActionResponse], error) {
	userID, id, err := h.actionCaller(ctx, req.Msg.GetProposalId())
	if err != nil {
		return nil, err
	}
	var option *int
	if req.Msg.OptionIndex != nil {
		v := int(req.Msg.GetOptionIndex())
		option = &v
	}
	res, err := h.svc.Confirm(ctx, userID, id, option)
	if err != nil {
		return nil, h.actionError(ctx, err)
	}
	return connect.NewResponse(res), nil
}

// DismissAction drops a card.
func (h *Handler) DismissAction(
	ctx context.Context,
	req *connect.Request[assistantv1.DismissActionRequest],
) (*connect.Response[assistantv1.DismissActionResponse], error) {
	userID, id, err := h.actionCaller(ctx, req.Msg.GetProposalId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Dismiss(ctx, userID, id); err != nil {
		return nil, h.actionError(ctx, err)
	}
	return connect.NewResponse(&assistantv1.DismissActionResponse{}), nil
}

func (h *Handler) actionCaller(ctx context.Context, proposalID string) (userID, id uuid.UUID, err error) {
	if h.svc == nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnimplemented, errors.New("the assistant is not available"))
	}
	raw, ok := interceptors.GetUserIDFromContext(ctx)
	userID, err = uuid.Parse(raw)
	if !ok || err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("sign in to do that"))
	}
	id, err = uuid.Parse(proposalID)
	if err != nil {
		return uuid.Nil, uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid proposal ID"))
	}
	return userID, id, nil
}

// actionError matches what ApplyTripAction answers, so clients treat a card the
// same whichever screen it is on.
func (h *Handler) actionError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, tripaction.ErrNotFound), errors.Is(err, trip.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, tripaction.ErrNotPending), errors.Is(err, tripaction.ErrExpired), errors.Is(err, trip.ErrVersionConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, tripaction.ErrNoOption), errors.Is(err, tripaction.ErrInvalid), errors.Is(err, trip.ErrInvalidEdit):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, tripaction.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, tripaction.ErrTogetherOff):
		return connect.NewError(connect.CodeUnimplemented, err)
	default:
		h.logger.ErrorContext(ctx, "assistant: action failed", slog.Any("error", err))
		return connect.NewError(connect.CodeInternal, errors.New("the change could not be applied"))
	}
}
