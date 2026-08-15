package handler

import (
	"context"

	"connectrpc.com/connect"
	tagsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/tags"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/tags/tagsv1connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/tags/presenter"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Service defines the interface for tag operations
type Service interface {
	GetTags(ctx context.Context, userID uuid.UUID) ([]*locitypes.Tags, error)
	GetTag(ctx context.Context, userID, tagID uuid.UUID) (*locitypes.Tags, error)
	CreateTag(ctx context.Context, userID uuid.UUID, params locitypes.CreatePersonalTagParams) (*locitypes.PersonalTag, error)
	DeleteTag(ctx context.Context, userID, tagID uuid.UUID) error
	Update(ctx context.Context, userID, tagID uuid.UUID, params locitypes.UpdatePersonalTagParams) error
}

type TagsHandler struct {
	tagsv1connect.UnimplementedTagsServiceHandler
	service Service
}

func NewTagsHandler(svc Service) *TagsHandler {
	return &TagsHandler{service: svc}
}

func (h *TagsHandler) GetTags(ctx context.Context, req *connect.Request[tagsv1.GetTagsRequest]) (*connect.Response[tagsv1.GetTagsResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	tagsList, err := h.service.GetTags(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&tagsv1.GetTagsResponse{
		Tags: presenter.ToTagProtos(tagsList),
	}), nil
}

func (h *TagsHandler) GetTag(ctx context.Context, req *connect.Request[tagsv1.GetTagRequest]) (*connect.Response[tagsv1.GetTagResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	tagID, err := uuid.Parse(req.Msg.TagId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	tag, err := h.service.GetTag(ctx, userID, tagID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&tagsv1.GetTagResponse{
		Tag: presenter.ToTagProto(tag),
	}), nil
}

func (h *TagsHandler) CreateTag(ctx context.Context, req *connect.Request[tagsv1.CreateTagRequest]) (*connect.Response[tagsv1.CreateTagResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	params := presenter.FromCreateRequest(req.Msg)
	tag, err := h.service.CreateTag(ctx, userID, params)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&tagsv1.CreateTagResponse{
		Tag: presenter.ToPersonalTagProto(tag),
	}), nil
}

func (h *TagsHandler) UpdateTag(ctx context.Context, req *connect.Request[tagsv1.UpdateTagRequest]) (*connect.Response[tagsv1.UpdateTagResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	tagID, err := uuid.Parse(req.Msg.TagId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	params := presenter.FromUpdateRequest(req.Msg)
	if err := h.service.Update(ctx, userID, tagID, params); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&tagsv1.UpdateTagResponse{
		Success: true,
	}), nil
}

func (h *TagsHandler) DeleteTag(ctx context.Context, req *connect.Request[tagsv1.DeleteTagRequest]) (*connect.Response[tagsv1.DeleteTagResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	tagID, err := uuid.Parse(req.Msg.TagId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := h.service.DeleteTag(ctx, userID, tagID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&tagsv1.DeleteTagResponse{
		Success: true,
	}), nil
}
