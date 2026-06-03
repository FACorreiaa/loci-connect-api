package handler

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	listpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/list"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/list/listv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	itinerarylist "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// ListHandler implements the ListService Connect handlers over the existing
// list service. Only the methods the client consumes are implemented; the rest
// fall through to UnimplementedListServiceHandler.
type ListHandler struct {
	listv1connect.UnimplementedListServiceHandler
	service itinerarylist.Service
	logger  *slog.Logger
}

func NewListHandler(svc itinerarylist.Service, logger *slog.Logger) *ListHandler {
	return &ListHandler{service: svc, logger: logger.With(slog.String("component", "list-handler"))}
}

func userFromCtx(ctx context.Context) (uuid.UUID, error) {
	idStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user id in token"))
	}
	return id, nil
}

func (h *ListHandler) CreateList(ctx context.Context, req *connect.Request[listpb.CreateListRequest]) (*connect.Response[listpb.CreateListResponse], error) {
	userID, err := userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	var cityID *uuid.UUID
	if req.Msg.CityId != "" {
		if id, e := uuid.Parse(req.Msg.CityId); e == nil {
			cityID = &id
		}
	}
	l, err := h.service.CreateTopLevelList(ctx, userID, req.Msg.Name, req.Msg.Description, cityID, req.Msg.IsItinerary, req.Msg.IsPublic)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&listpb.CreateListResponse{Success: true, List: toProtoList(l)}), nil
}

func (h *ListHandler) GetLists(ctx context.Context, req *connect.Request[listpb.GetListsRequest]) (*connect.Response[listpb.GetListsResponse], error) {
	userID, err := userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	lists, err := h.service.GetUserLists(ctx, userID, false)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*listpb.ListWithItems, 0, len(lists))
	for _, l := range lists {
		out = append(out, &listpb.ListWithItems{List: toProtoList(l)})
	}
	return connect.NewResponse(&listpb.GetListsResponse{Lists: out, TotalCount: int32(len(out))}), nil
}

func (h *ListHandler) GetList(ctx context.Context, req *connect.Request[listpb.GetListRequest]) (*connect.Response[listpb.GetListResponse], error) {
	userID, err := userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	listID, err := uuid.Parse(req.Msg.ListId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid list id"))
	}
	lwi, err := h.service.GetListDetails(ctx, listID, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if lwi == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("list not found"))
	}
	items := make([]*listpb.ListItemWithContent, 0, len(lwi.Items))
	for _, it := range lwi.Items {
		items = append(items, &listpb.ListItemWithContent{ListItem: toProtoListItem(it)})
	}
	return connect.NewResponse(&listpb.GetListResponse{
		List: &listpb.ListWithDetailedItems{List: toProtoList(&lwi.List), Items: items},
	}), nil
}

func (h *ListHandler) UpdateList(ctx context.Context, req *connect.Request[listpb.UpdateListRequest]) (*connect.Response[listpb.UpdateListResponse], error) {
	userID, err := userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	listID, err := uuid.Parse(req.Msg.ListId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid list id"))
	}
	params := locitypes.UpdateListRequest{IsPublic: &req.Msg.IsPublic}
	if req.Msg.Name != "" {
		params.Name = &req.Msg.Name
	}
	if req.Msg.Description != "" {
		params.Description = &req.Msg.Description
	}
	if req.Msg.ImageUrl != "" {
		params.ImageURL = &req.Msg.ImageUrl
	}
	if req.Msg.CityId != "" {
		if id, e := uuid.Parse(req.Msg.CityId); e == nil {
			params.CityID = &id
		}
	}
	l, err := h.service.UpdateListDetails(ctx, listID, userID, params)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&listpb.UpdateListResponse{Success: true, List: toProtoList(l)}), nil
}

func (h *ListHandler) DeleteList(ctx context.Context, req *connect.Request[listpb.DeleteListRequest]) (*connect.Response[listpb.DeleteListResponse], error) {
	userID, err := userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	listID, err := uuid.Parse(req.Msg.ListId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid list id"))
	}
	if err := h.service.DeleteUserList(ctx, listID, userID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&listpb.DeleteListResponse{Success: true}), nil
}

// --- presenters ---

func toProtoList(l *locitypes.List) *listpb.List {
	if l == nil {
		return nil
	}
	p := &listpb.List{
		Id:          l.ID.String(),
		UserId:      l.UserID.String(),
		Name:        l.Name,
		Description: l.Description,
		ImageUrl:    l.ImageURL,
		IsPublic:    l.IsPublic,
		IsItinerary: l.IsItinerary,
		CityId:      l.CityID.String(),
		ViewCount:   int32(l.ViewCount),
		SaveCount:   int32(l.SaveCount),
		CreatedAt:   timestamppb.New(l.CreatedAt),
		UpdatedAt:   timestamppb.New(l.UpdatedAt),
	}
	if l.ParentListID != nil {
		p.ParentListId = l.ParentListID.String()
	}
	return p
}

func toProtoListItem(i *locitypes.ListItem) *listpb.ListItem {
	if i == nil {
		return nil
	}
	it := &listpb.ListItem{
		ListId:            i.ListID.String(),
		ItemId:            i.ItemID.String(),
		PoiId:             i.PoiID.String(),
		Position:          int32(i.Position),
		Notes:             i.Notes,
		CreatedAt:         timestamppb.New(i.CreatedAt),
		UpdatedAt:         timestamppb.New(i.UpdatedAt),
		ItemAiDescription: i.ItemAIDescription,
	}
	if i.DayNumber != nil {
		it.DayNumber = int32(*i.DayNumber)
	}
	if i.TimeSlot != nil {
		it.TimeSlot = timestamppb.New(*i.TimeSlot)
	}
	if i.Duration != nil {
		it.Duration = int32(*i.Duration)
	}
	if i.SourceLlmInteractionID != nil {
		it.SourceLlmInteractionId = i.SourceLlmInteractionID.String()
	}
	return it
}
