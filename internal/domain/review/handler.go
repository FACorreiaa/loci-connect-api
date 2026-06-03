package review

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/common"
	reviewv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/review"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/review/reviewv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements the ReviewService Connect handlers. Unimplemented RPCs
// (UpdateReview, ReportReview, GetReviewStatistics, GetContentReviews) fall
// through to the embedded default.
type Handler struct {
	reviewv1connect.UnimplementedReviewServiceHandler
	service Service
	logger  *slog.Logger
}

func NewHandler(svc Service, logger *slog.Logger) *Handler {
	return &Handler{service: svc, logger: logger.With(slog.String("component", "review-handler"))}
}

func ctxUser(ctx context.Context) (uuid.UUID, error) {
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

func (h *Handler) toConnectError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrInvalidReview):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (h *Handler) CreateReview(ctx context.Context, req *connect.Request[reviewv1.CreateReviewRequest]) (*connect.Response[reviewv1.CreateReviewResponse], error) {
	userID, err := ctxUser(ctx)
	if err != nil {
		return nil, err
	}
	poiID, err := uuid.Parse(req.Msg.PoiId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid poi_id"))
	}
	in := CreateReviewInput{
		UserID:  userID,
		POIID:   poiID,
		Rating:  int(req.Msg.Rating),
		Title:   req.Msg.Title,
		Content: req.Msg.Content,
		Photos:  req.Msg.PhotoUrls,
	}
	if req.Msg.VisitDate != nil {
		t := req.Msg.VisitDate.AsTime()
		in.VisitDate = &t
	}
	r, err := h.service.CreateReview(ctx, in)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.CreateReviewResponse{Response: okResponse(), Review: toProtoReview(r)}), nil
}

func (h *Handler) GetReview(ctx context.Context, req *connect.Request[reviewv1.GetReviewRequest]) (*connect.Response[reviewv1.GetReviewResponse], error) {
	id, err := uuid.Parse(req.Msg.ReviewId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid review_id"))
	}
	r, err := h.service.GetReview(ctx, id)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	owner := false
	if uid, e := ctxUser(ctx); e == nil {
		owner = uid == r.UserID
	}
	return connect.NewResponse(&reviewv1.GetReviewResponse{Review: toProtoReview(r), CanEdit: owner, CanDelete: owner}), nil
}

func (h *Handler) GetPOIReviews(ctx context.Context, req *connect.Request[reviewv1.GetPOIReviewsRequest]) (*connect.Response[reviewv1.GetPOIReviewsResponse], error) {
	poiID, err := uuid.Parse(req.Msg.PoiId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid poi_id"))
	}
	limit, offset := pageBounds(req.Msg.Pagination)
	list, total, err := h.service.ListPOIReviews(ctx, poiID, limit, offset)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.GetPOIReviewsResponse{
		Reviews:    toProtoReviews(list),
		Pagination: pageMeta(total, limit, offset),
	}), nil
}

func (h *Handler) GetUserReviews(ctx context.Context, req *connect.Request[reviewv1.GetUserReviewsRequest]) (*connect.Response[reviewv1.GetUserReviewsResponse], error) {
	userID, err := uuid.Parse(req.Msg.UserId)
	if err != nil {
		// Fall back to the authenticated user when no explicit id is given.
		userID, err = ctxUser(ctx)
		if err != nil {
			return nil, err
		}
	}
	limit, offset := pageBounds(req.Msg.Pagination)
	list, total, err := h.service.ListUserReviews(ctx, userID, limit, offset)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.GetUserReviewsResponse{
		Reviews:    toProtoReviews(list),
		Pagination: pageMeta(total, limit, offset),
	}), nil
}

func (h *Handler) DeleteReview(ctx context.Context, req *connect.Request[reviewv1.DeleteReviewRequest]) (*connect.Response[reviewv1.DeleteReviewResponse], error) {
	userID, err := ctxUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.ReviewId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid review_id"))
	}
	if err := h.service.DeleteReview(ctx, id, userID); err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.DeleteReviewResponse{Response: okResponse()}), nil
}

func (h *Handler) LikeReview(ctx context.Context, req *connect.Request[reviewv1.LikeReviewRequest]) (*connect.Response[reviewv1.LikeReviewResponse], error) {
	userID, err := ctxUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.ReviewId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid review_id"))
	}
	count, err := h.service.LikeReview(ctx, userID, id, req.Msg.IsLike)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.LikeReviewResponse{Response: okResponse(), NewHelpfulCount: int32(count)}), nil
}

// --- mapping helpers ---

func okResponse() *commonpb.Response {
	return &commonpb.Response{Success: true}
}

func pageBounds(p *commonpb.PaginationRequest) (limit, offset int) {
	if p == nil || p.PageSize <= 0 {
		return 20, 0
	}
	limit = int(p.PageSize)
	if limit > 100 {
		limit = 100
	}
	page := int(p.Page)
	if page < 1 {
		page = 1
	}
	return limit, (page - 1) * limit
}

func pageMeta(total, limit, offset int) *commonpb.PaginationMetadata {
	if limit <= 0 {
		limit = 20
	}
	page := offset/limit + 1
	totalPages := (total + limit - 1) / limit
	return &commonpb.PaginationMetadata{
		TotalRecords: int32(total),
		Page:         int32(page),
		PageSize:     int32(limit),
		TotalPages:   int32(totalPages),
		HasMore:      offset+limit < total,
	}
}

func toProtoReview(r *Review) *reviewv1.Review {
	if r == nil {
		return nil
	}
	status := reviewv1.ReviewStatus_REVIEW_STATUS_PUBLISHED
	if !r.IsPublished {
		status = reviewv1.ReviewStatus_REVIEW_STATUS_PENDING
	}
	p := &reviewv1.Review{
		Id:           r.ID.String(),
		UserId:       r.UserID.String(),
		PoiId:        r.POIID.String(),
		Rating:       float64(r.Rating),
		Title:        r.Title,
		Content:      r.Content,
		Photos:       r.Photos,
		Status:       status,
		CreatedAt:    timestamppb.New(r.CreatedAt),
		UpdatedAt:    timestamppb.New(r.UpdatedAt),
		HelpfulCount: int32(r.Helpful),
		IsVerified:   r.IsVerified,
	}
	if r.VisitDate != nil {
		p.VisitDate = timestamppb.New(*r.VisitDate)
	}
	return p
}

func toProtoReviews(list []*Review) []*reviewv1.Review {
	out := make([]*reviewv1.Review, 0, len(list))
	for _, r := range list {
		out = append(out, toProtoReview(r))
	}
	return out
}
