package handler

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	itineraryv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/itinerary"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/itinerary/itineraryconnect"

	chatservice "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	listservice "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type ItineraryHandler struct {
	itineraryconnect.UnimplementedItineraryServiceHandler
	listService listservice.Service
	chatService chatservice.LlmInteractiontService
	logger      *slog.Logger
}

func NewItineraryHandler(
	listService listservice.Service,
	chatService chatservice.LlmInteractiontService,
	logger *slog.Logger,
) *ItineraryHandler {
	return &ItineraryHandler{
		listService: listService,
		chatService: chatService,
		logger:      logger,
	}
}

// BookmarkItinerary implements the bookmarking logic utilizing ChatService
func (h *ItineraryHandler) BookmarkItinerary(ctx context.Context, req *connect.Request[itineraryv1.BookmarkRequest]) (*connect.Response[commonpb.Response], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	// Map proto request to domain request
	domainReq := locitypes.BookmarkRequest{
		Title:           req.Msg.Title,
		PrimaryCityName: req.Msg.PrimaryCityName,
		Tags:            req.Msg.Tags,
	}

	if req.Msg.LlmInteractionId != nil {
		if id, err := uuid.Parse(*req.Msg.LlmInteractionId); err == nil {
			domainReq.LlmInteractionID = &id
		}
	}

	if req.Msg.SessionId != nil {
		if id, err := uuid.Parse(*req.Msg.SessionId); err == nil {
			domainReq.SessionID = &id
		}
	}

	if req.Msg.PrimaryCityId != nil {
		if id, err := uuid.Parse(*req.Msg.PrimaryCityId); err == nil {
			domainReq.PrimaryCityID = &id
		}
	}

	if req.Msg.Description != nil {
		domainReq.Description = req.Msg.Description
	}

	if req.Msg.IsPublic != nil {
		domainReq.IsPublic = req.Msg.IsPublic
	}

	// Call ChatService to save the interaction as an itinerary/bookmark
	savedID, err := h.chatService.SaveItineraryFromInteraction(ctx, userID, domainReq)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "itinerary bookmarked successfully: " + savedID.String()
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
}

// Delegate other methods to ListService where appropriate...
// For now, implementing BookmarkItinerary as primary task.
