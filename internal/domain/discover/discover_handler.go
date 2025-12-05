package discover

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/discover/presenter"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"

	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	discoverv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/discover"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/discover/discoverconnect"
)

const defaultPageSize = 10

// Handler implements the DiscoverService RPCs.
type Handler struct {
	discoverconnect.UnimplementedDiscoverServiceHandler
	svc    Service
	logger *slog.Logger
}

// NewHandler wires a Discover handler.
func NewHandler(svc Service, logger *slog.Logger) *Handler {
	return &Handler{
		svc:    svc,
		logger: logger,
	}
}

// GetDiscoverPage returns the aggregated discover page data (trending, featured, recent).
func (h *Handler) GetDiscoverPage(
	ctx context.Context,
	_ *connect.Request[discoverv1.GetDiscoverPageRequest],
) (*connect.Response[discoverv1.GetDiscoverPageResponse], error) {
	userID := h.userIDFromContext(ctx)

	data, err := h.svc.GetDiscoverPageData(ctx, userID, defaultPageSize)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&discoverv1.GetDiscoverPageResponse{
		Data: presenter.ToDiscoverPageData(data),
	}), nil
}

// GetTrending returns trending discoveries (by city/query count).
func (h *Handler) GetTrending(
	ctx context.Context,
	req *connect.Request[discoverv1.GetTrendingRequest],
) (*connect.Response[discoverv1.GetTrendingResponse], error) {
	limit := defaultPageSize
	if req.Msg.GetLimit() > 0 {
		limit = int(req.Msg.GetLimit())
	}

	trending, err := h.svc.GetTrendingDiscoveries(ctx, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&discoverv1.GetTrendingResponse{
		Trending: presenter.ToTrendingDiscoveries(trending),
	}), nil
}

// GetFeatured returns featured collections.
func (h *Handler) GetFeatured(
	ctx context.Context,
	req *connect.Request[discoverv1.GetFeaturedRequest],
) (*connect.Response[discoverv1.GetFeaturedResponse], error) {
	limit := defaultPageSize
	if req.Msg.GetLimit() > 0 {
		limit = int(req.Msg.GetLimit())
	}

	featured, err := h.svc.GetFeaturedCollections(ctx, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&discoverv1.GetFeaturedResponse{
		Featured: presenter.ToFeaturedCollections(featured),
	}), nil
}

// GetRecentDiscoveries returns the user's recent discovery sessions.
func (h *Handler) GetRecentDiscoveries(
	ctx context.Context,
	req *connect.Request[discoverv1.GetRecentDiscoveriesRequest],
) (*connect.Response[discoverv1.GetRecentDiscoveriesResponse], error) {
	userID := h.userIDFromContext(ctx)

	// Allow explicitly provided user_id only when no authenticated user is present.
	if userID == uuid.Nil && req.Msg.GetUserId() != "" {
		parsed, err := uuid.Parse(req.Msg.GetUserId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user_id"))
		}
		userID = parsed
	}

	if userID == uuid.Nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	page, pageSize := paginationParamsCommon(req.Msg.GetPagination())
	recent, total, err := h.svc.GetRecentDiscoveries(ctx, userID, page, pageSize)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &discoverv1.GetRecentDiscoveriesResponse{
		Sessions:   presenter.ToChatSessions(recent),
		Pagination: presenter.ToPaginationMetadata(total, page, pageSize),
	}

	return connect.NewResponse(resp), nil
}

// GetCategoryResults returns POIs within a category, optionally city-scoped.
func (h *Handler) GetCategoryResults(
	ctx context.Context,
	req *connect.Request[discoverv1.GetCategoryResultsRequest],
) (*connect.Response[discoverv1.GetCategoryResultsResponse], error) {
	if req.Msg.GetCategory() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("category is required"))
	}

	page, pageSize := paginationParamsCommon(req.Msg.GetPagination())
	cityName := req.Msg.GetCityName()

	results, err := h.svc.GetCategoryResults(ctx, req.Msg.GetCategory(), cityName, page, pageSize)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &discoverv1.GetCategoryResultsResponse{
		Category:   req.Msg.GetCategory(),
		Results:    presenter.ToDiscoverResults(results),
		Pagination: presenter.ToPaginationMetadata(len(results), page, pageSize),
	}

	return connect.NewResponse(resp), nil
}

func (h *Handler) userIDFromContext(ctx context.Context) uuid.UUID {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return uuid.Nil
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("invalid user ID in context", slog.Any("error", err))
		return uuid.Nil
	}
	return userID
}

func paginationParamsCommon(p *commonpb.PaginationRequest) (page, pageSize int) {
	page = 1
	pageSize = defaultPageSize
	if p != nil {
		if p.GetPage() > 0 {
			page = int(p.GetPage())
		}
		if p.GetPageSize() > 0 {
			pageSize = int(p.GetPageSize())
		}
	}
	return
}
