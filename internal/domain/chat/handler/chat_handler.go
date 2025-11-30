package handler

import (
	"context"

	"connectrpc.com/connect"
	chat "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat"
	authconnect "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat/chatconnect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
)

type ChatHandler struct {
	authconnect.UnimplementedChatServiceHandler
	service *service.LlmInteractiontService
}

func NewChatHandler(svc *service.LlmInteractiontService) *ChatHandler {
	return &ChatHandler{
		service: svc,
	}
}

func (h *ChatHandler) StartChat(
	ctx context.Context,
	req *connect.Request[chat.StartChatRequest],
) (*connect.Response[commonpb.Response], error) {
	return nil, nil
}

func (h *ChatHandler) ContinueChat(
	ctx context.Context,
	req *connect.Request[chat.ContinueChatRequest],
) (*connect.Response[commonpb.Response], error) {
	return nil, nil
}

func (h *ChatHandler) GetChatSession(
	ctx context.Context,
	req *connect.Request[chat.GetChatSessionRequest],
) (*connect.Response[commonpb.Response], error) {
	return nil, nil
}

func (h *ChatHandler) GetChatSessions(
	ctx context.Context,
	req *connect.Request[chat.GetChatSessionsRequest],
) (*connect.Response[commonpb.Response], error) {
	return nil, nil
}
