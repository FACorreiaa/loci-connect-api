package assistant

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant/assistantconnect"

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
