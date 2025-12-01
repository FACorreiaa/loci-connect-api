package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat/chatconnect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/presenter"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	"github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// ChatHandler implements the ChatServiceHandler interface.
type ChatHandler struct {
	chatconnect.UnimplementedChatServiceHandler
	service service.LlmInteractiontService
	logger  *slog.Logger
}

// NewChatHandler creates a new ChatHandler.
func NewChatHandler(llmInteractionService service.LlmInteractiontService, logger *slog.Logger) *ChatHandler {
	return &ChatHandler{
		service: llmInteractionService,
		logger:  logger,
	}
}

func (h *ChatHandler) StartChat(
	ctx context.Context,
	req *connect.Request[chatv1.StartChatRequest],
) (*connect.Response[chatv1.ChatResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	resp, err := h.service.StartChat(ctx, userID, uuid.Nil, req.Msg.GetCityName(), req.Msg.GetInitialMessage(), nil)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.ToChatResponse(resp)), nil
}

// StreamChat handles the streaming chat RPC.
func (h *ChatHandler) StreamChat(
	ctx context.Context,
	req *connect.Request[chatv1.ChatRequest],
	stream *connect.ServerStream[chatv1.StreamEvent],
) error {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	eventCh := make(chan types.StreamEvent, 100)

	go func() {
		defer close(eventCh)
		err := h.service.ProcessUnifiedChatMessageStream(
			ctx,
			userID,
			uuid.Nil,
			req.Msg.GetCityName(),
			req.Msg.Message,
			nil,
			eventCh,
		)
		if err != nil {
			select {
			case eventCh <- types.StreamEvent{Type: types.EventTypeError, Error: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return nil // Stream finished successfully
			}

			resp, err := h.mapEventToProto(event)
			if err != nil {
				h.logger.Error("Failed to map event", "error", err)
				continue
			}

			if err := stream.Send(resp); err != nil {
				return err
			}

			if event.Type == types.EventTypeComplete || event.Type == types.EventTypeError {
				return nil
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (h *ChatHandler) toConnectError(err error) error {
	switch {
	case errors.Is(err, common.ErrChatNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, common.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, common.ErrInvalidInput):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, common.ErrUnauthorized):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, common.ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, common.ErrInvalidUUID):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, common.ErrItineraryNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (h *ChatHandler) mapEventToProto(event types.StreamEvent) (*chatv1.StreamEvent, error) {
	resp := &chatv1.StreamEvent{
		Type:      string(event.Type),
		Message:   event.Message,
		Timestamp: timestamppb.New(event.Timestamp),
		EventId:   event.EventID,
		IsFinal:   event.IsFinal,
	}

	if event.Error != "" {
		resp.Error = &event.Error
	}

	if event.Navigation != nil {
		resp.Navigation = &chatv1.NavigationData{
			Url:         event.Navigation.URL,
			RouteType:   event.Navigation.RouteType,
			QueryParams: event.Navigation.QueryParams,
		}
	}

	if event.Data != nil {
		dataBytes, err := json.Marshal(event.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal event data: %w", err)
		}
		resp.Data = dataBytes
	}

	return resp, nil
}

func (h *ChatHandler) ContinueChat(
	ctx context.Context,
	req *connect.Request[chatv1.ContinueChatRequest],
) (*connect.Response[chatv1.ChatResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session ID"))
	}

	resp, err := h.service.ContinueChat(ctx, userID, sessionID, req.Msg.GetMessage(), req.Msg.GetCityName())
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.ToChatResponse(resp)), nil
}

func (h *ChatHandler) GetChatSession(
	ctx context.Context,
	req *connect.Request[chatv1.GetChatSessionRequest],
) (*connect.Response[chatv1.GetChatSessionResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session ID"))
	}

	session, err := h.service.GetChatSession(ctx, userID, sessionID)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(&chatv1.GetChatSessionResponse{
		Session: presenter.ToChatSession(session),
	}), nil
}

func (h *ChatHandler) GetChatSessions(
	ctx context.Context,
	req *connect.Request[chatv1.GetChatSessionsRequest],
) (*connect.Response[chatv1.GetChatSessionsResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	page := int(req.Msg.GetPagination().GetPage())
	limit := int(req.Msg.GetPagination().GetPageSize())
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}

	result, err := h.service.GetUserChatSessions(ctx, userID, page, limit)
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(presenter.ToGetChatSessionsResponse(result)), nil
}

func (h *ChatHandler) GetRecentInteractions(
	ctx context.Context,
	req *connect.Request[chatv1.GetRecentInteractionsRequest],
) (*connect.Response[chatv1.GetRecentInteractionsResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	resp, err := h.service.GetRecentInteractions(ctx, userID, req.Msg.GetPagination())
	if err != nil {
		return nil, h.toConnectError(err)
	}

	return connect.NewResponse(resp), nil
}

func (h *ChatHandler) EndSession(
	ctx context.Context,
	req *connect.Request[chatv1.GetChatSessionRequest],
) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session ID"))
	}

	if err := h.service.EndSession(ctx, userID, sessionID); err != nil {
		return nil, h.toConnectError(err)
	}

	msg := "session ended"
	return connect.NewResponse(&commonpb.Response{Success: true, Message: &msg}), nil
}
