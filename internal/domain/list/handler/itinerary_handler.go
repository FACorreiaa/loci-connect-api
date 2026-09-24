package handler

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	itineraryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/itinerary"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/itinerary/itineraryconnect"

	chatservice "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/service"
	listservice "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Matches the proto's page_size ceiling (buf.validate lte: 100).
const defaultItineraryPageSize = 100

type ItineraryHandler struct {
	itineraryconnect.UnimplementedItineraryServiceHandler
	listService listservice.Service
	chatService chatservice.LlmInteractiontService
	itineraries ItineraryReader
	logger      *slog.Logger
}

// ItineraryReader reads one of a user's saved itineraries. It reports a
// missing or foreign itinerary as an error wrapping pgx.ErrNoRows.
type ItineraryReader interface {
	GetItinerary(ctx context.Context, userID, itineraryID uuid.UUID) (*locitypes.UserSavedItinerary, error)
}

// WithItineraries enables GetItinerary.
func (h *ItineraryHandler) WithItineraries(r ItineraryReader) *ItineraryHandler {
	h.itineraries = r
	return h
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
	userID, err := h.callerID(ctx)
	if err != nil {
		return nil, err
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

// GetUserItineraries lists the itineraries this user has bookmarked, newest first.
func (h *ItineraryHandler) GetUserItineraries(ctx context.Context, req *connect.Request[itineraryv1.GetUserItinerariesRequest]) (*connect.Response[itineraryv1.GetUserItinerariesResponse], error) {
	userID, err := h.callerID(ctx)
	if err != nil {
		return nil, err
	}

	// The request's user_id is ignored: the token decides whose bookmarks these are.
	page, pageSize := 1, defaultItineraryPageSize
	if p := req.Msg.Pagination; p != nil {
		if p.Page > 0 {
			page = int(p.Page)
		}
		if p.PageSize > 0 {
			pageSize = int(p.PageSize)
		}
	}

	saved, err := h.chatService.GetBookmarkedItineraries(ctx, userID, page, pageSize)
	if err != nil {
		h.logger.ErrorContext(ctx, "could not list bookmarked itineraries", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	out := make([]*itineraryv1.UserSavedItinerary, 0, len(saved.Itineraries))
	for i := range saved.Itineraries {
		out = append(out, itineraryToProto(&saved.Itineraries[i]))
	}

	return connect.NewResponse(&itineraryv1.GetUserItinerariesResponse{
		Itineraries: out,
		Pagination:  paginationMeta(saved.Page, saved.PageSize, saved.TotalRecords),
	}), nil
}

// GetItinerary returns one of this user's saved itineraries. Another user's
// id answers NotFound, not PermissionDenied, so ids cannot be probed.
func (h *ItineraryHandler) GetItinerary(ctx context.Context, req *connect.Request[itineraryv1.GetItineraryRequest]) (*connect.Response[itineraryv1.GetItineraryResponse], error) {
	userID, err := h.callerID(ctx)
	if err != nil {
		return nil, err
	}
	itineraryID, err := uuid.Parse(req.Msg.ItineraryId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid itinerary ID"))
	}
	if h.itineraries == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("itineraries are not available"))
	}

	it, err := h.itineraries.GetItinerary(ctx, userID, itineraryID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && it == nil) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("itinerary not found"))
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "could not read itinerary", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("could not read itinerary"))
	}
	return connect.NewResponse(&itineraryv1.GetItineraryResponse{Itinerary: itineraryToProto(it)}), nil
}

// DeleteBookmark removes one of this user's bookmarked itineraries.
func (h *ItineraryHandler) DeleteBookmark(ctx context.Context, req *connect.Request[itineraryv1.DeleteBookmarkRequest]) (*connect.Response[commonpb.Response], error) {
	userID, err := h.callerID(ctx)
	if err != nil {
		return nil, err
	}

	itineraryID, err := uuid.Parse(req.Msg.ItineraryId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid itinerary ID"))
	}

	if err := h.chatService.RemoveItinerary(ctx, userID, itineraryID); err != nil {
		h.logger.ErrorContext(ctx, "could not remove bookmarked itinerary", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	msg := "itinerary bookmark removed: " + itineraryID.String()
	return connect.NewResponse(&commonpb.Response{
		Success: true,
		Message: &msg,
	}), nil
}

// callerID is the authenticated user; every itinerary procedure is user-scoped.
func (h *ItineraryHandler) callerID(ctx context.Context) (uuid.UUID, error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return userID, nil
}

func paginationMeta(page, pageSize, total int) *commonpb.PaginationMetadata {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return &commonpb.PaginationMetadata{
		TotalRecords: int32(total),
		Page:         int32(page),
		PageSize:     int32(pageSize),
		TotalPages:   int32(totalPages),
		HasMore:      page < totalPages,
	}
}

func itineraryToProto(in *locitypes.UserSavedItinerary) *itineraryv1.UserSavedItinerary {
	out := &itineraryv1.UserSavedItinerary{
		Id:              in.ID.String(),
		UserId:          in.UserID.String(),
		Title:           in.Title,
		MarkdownContent: in.MarkdownContent,
		Tags:            in.Tags,
		IsPublic:        in.IsPublic,
		CreatedAt:       timestamppb.New(in.CreatedAt),
		UpdatedAt:       timestamppb.New(in.UpdatedAt),
	}
	if in.SourceLlmInteractionID != nil {
		out.SourceLlmInteractionId = proto.String(in.SourceLlmInteractionID.String())
	}
	if in.SessionID != nil {
		out.SessionId = proto.String(in.SessionID.String())
	}
	if in.PrimaryCityID != nil {
		out.PrimaryCityId = proto.String(in.PrimaryCityID.String())
	}
	if in.Description != nil && *in.Description != "" {
		out.Description = proto.String(*in.Description)
	}
	out.EstimatedDurationDays = in.EstimatedDurationDays
	out.EstimatedCostLevel = in.EstimatedCostLevel
	return out
}
