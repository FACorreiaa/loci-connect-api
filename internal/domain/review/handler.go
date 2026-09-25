package review

import (
	"context"
	"errors"
	"log/slog"
	"math"

	"connectrpc.com/connect"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	reviewv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/review"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/review/reviewv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements the ReviewService Connect handlers. ReportReview falls
// through to the embedded default (Unimplemented): there is nowhere to store
// a report yet.
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
	case errors.Is(err, ErrInvalidReview), errors.Is(err, ErrPOINotFound):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, ErrOwnReview):
		return connect.NewError(connect.CodePermissionDenied, err)
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
	rating, err := ratingFromProto(req.Msg.Rating)
	if err != nil {
		return nil, err
	}
	in := CreateReviewInput{
		UserID:  userID,
		POIID:   poiID,
		Rating:  rating,
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
	var userID uuid.UUID
	var err error
	if req.Msg.UserId == "" {
		// No explicit id: the caller's own reviews.
		userID, err = ctxUser(ctx)
		if err != nil {
			return nil, err
		}
	} else if userID, err = uuid.Parse(req.Msg.UserId); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user_id"))
	}
	limit, offset := pageBounds(req.Msg.Pagination)
	list, total, err := h.service.ListUserReviews(ctx, userID, limit, offset)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	st, err := h.service.GetUserStatistics(ctx, userID)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.GetUserReviewsResponse{
		Reviews:    toProtoReviews(list),
		Pagination: pageMeta(total, limit, offset),
		Statistics: &reviewv1.UserReviewStatistics{
			TotalReviews:         int32(st.TotalReviews), //nolint:gosec // bounded by the row count
			AverageRatingGiven:   st.AverageRatingGiven,
			HelpfulVotesReceived: int32(st.HelpfulVotesReceived), //nolint:gosec // bounded by the vote count
			ReviewerLevel:        reviewerLevel(st.TotalReviews),
		},
	}), nil
}

// reviewerLevel names the tier My reviews shows for a review count. The
// thresholds are the ones the web client used before the server filled this.
func reviewerLevel(total int) string {
	switch {
	case total >= 20:
		return "expert"
	case total >= 5:
		return "guide"
	case total >= 1:
		return "explorer"
	default:
		return "new"
	}
}

func (h *Handler) GetRecentReviews(ctx context.Context, req *connect.Request[reviewv1.GetRecentReviewsRequest]) (*connect.Response[reviewv1.GetRecentReviewsResponse], error) {
	limit, offset := pageBounds(req.Msg.Pagination)
	list, total, err := h.service.ListRecentReviews(ctx, limit, offset)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.GetRecentReviewsResponse{
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

// UpdateReview replaces the caller's own review. The request's user_id is
// ignored; someone else's review is NotFound.
func (h *Handler) UpdateReview(ctx context.Context, req *connect.Request[reviewv1.UpdateReviewRequest]) (*connect.Response[reviewv1.UpdateReviewResponse], error) {
	userID, err := ctxUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.ReviewId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid review_id"))
	}
	rating, err := ratingFromProto(req.Msg.Rating)
	if err != nil {
		return nil, err
	}
	in := UpdateReviewInput{
		ReviewID: id,
		UserID:   userID,
		Rating:   rating,
		Title:    req.Msg.Title,
		Content:  req.Msg.Content,
		Photos:   req.Msg.PhotoUrls,
	}
	if req.Msg.VisitDate != nil {
		t := req.Msg.VisitDate.AsTime()
		in.VisitDate = &t
	}
	r, err := h.service.UpdateReview(ctx, in)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.UpdateReviewResponse{Response: okResponse(), Review: toProtoReview(r)}), nil
}

// GetReviewStatistics returns the count, average and star breakdown of a
// POI's published reviews. Trends, tags, aspects and languages are not
// computed; include_trends and include_tags are ignored.
func (h *Handler) GetReviewStatistics(ctx context.Context, req *connect.Request[reviewv1.GetReviewStatisticsRequest]) (*connect.Response[reviewv1.GetReviewStatisticsResponse], error) {
	poiID, err := uuid.Parse(req.Msg.PoiId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid poi_id"))
	}
	st, err := h.service.GetStatistics(ctx, poiID)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.GetReviewStatisticsResponse{Statistics: toProtoStatistics(st)}), nil
}

// GetContentReviews serves POI reviews (content_type POI or unspecified),
// with statistics. Reviews only exist for POIs, so any other content type is
// InvalidArgument.
func (h *Handler) GetContentReviews(ctx context.Context, req *connect.Request[reviewv1.GetContentReviewsRequest]) (*connect.Response[reviewv1.GetContentReviewsResponse], error) {
	switch req.Msg.ContentType {
	case reviewv1.ReviewContentType_REVIEW_CONTENT_TYPE_UNSPECIFIED, reviewv1.ReviewContentType_REVIEW_CONTENT_TYPE_POI:
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only POI reviews are supported"))
	}
	poiID, err := uuid.Parse(req.Msg.ContentId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid content_id"))
	}
	limit, offset := pageBounds(req.Msg.Pagination)
	list, total, err := h.service.ListPOIReviews(ctx, poiID, limit, offset)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	st, err := h.service.GetStatistics(ctx, poiID)
	if err != nil {
		return nil, h.toConnectError(err)
	}
	return connect.NewResponse(&reviewv1.GetContentReviewsResponse{
		Reviews:    toProtoReviews(list),
		Pagination: pageMeta(total, limit, offset),
		Statistics: toProtoStatistics(st),
	}), nil
}

// --- mapping helpers ---

// ratingFromProto accepts whole stars only. The column is an INTEGER 1–5, so
// a fractional rating used to be silently truncated (4.5 stored as 4).
func ratingFromProto(v float64) (int, error) {
	if v < 1 || v > 5 || v != math.Trunc(v) {
		return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("rating must be a whole number from 1 to 5"))
	}
	return int(v), nil
}

func toProtoStatistics(st *Statistics) *reviewv1.ReviewStatistics {
	if st == nil {
		return nil
	}
	p := &reviewv1.ReviewStatistics{
		PoiId:         st.POIID.String(),
		OverallRating: st.AverageRating,
		TotalReviews:  int32(st.TotalReviews),
		RatingBreakdown: &reviewv1.RatingBreakdown{
			OneStar:   int32(st.Distribution[0]),
			TwoStar:   int32(st.Distribution[1]),
			ThreeStar: int32(st.Distribution[2]),
			FourStar:  int32(st.Distribution[3]),
			FiveStar:  int32(st.Distribution[4]),
		},
	}
	if st.LastReviewAt != nil {
		p.LastUpdated = timestamppb.New(*st.LastReviewAt)
	}
	return p
}

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
		// Reviews are POI-only, so the generic content fields mirror poi_id.
		ContentType: reviewv1.ReviewContentType_REVIEW_CONTENT_TYPE_POI,
		ContentId:   r.POIID.String(),
		// Enrichment: POI name (content_name) + reviewer display info.
		ContentName: r.POIName,
		Reviewer: &reviewv1.ReviewerInfo{
			UserId:      r.UserID.String(),
			DisplayName: r.ReviewerName,
			AvatarUrl:   r.ReviewerAvatar,
			IsVerified:  r.IsVerified,
			MemberSince: timestamppb.New(r.ReviewerSince),
		},
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
