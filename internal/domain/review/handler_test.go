package review

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	reviewv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/review"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// fakeService records the inputs the handler passes down and returns canned
// results, so handler tests exercise only the request/response mapping.
type fakeService struct {
	Service // unimplemented methods panic

	created   *CreateReviewInput
	updated   *UpdateReviewInput
	listedFor uuid.UUID
	statsFor  uuid.UUID
	likedWith *bool
	err       error
	stats     *Statistics
	userStats *UserStatistics
}

func (f *fakeService) GetUserStatistics(_ context.Context, userID uuid.UUID) (*UserStatistics, error) {
	f.statsFor = userID
	if f.err != nil {
		return nil, f.err
	}
	if f.userStats == nil {
		return &UserStatistics{}, nil // the repository never returns nil without an error
	}
	return f.userStats, nil
}

func (f *fakeService) CreateReview(_ context.Context, in CreateReviewInput) (*Review, error) {
	f.created = &in
	if f.err != nil {
		return nil, f.err
	}
	return &Review{ID: uuid.New(), UserID: in.UserID, POIID: in.POIID, Rating: in.Rating, Content: in.Content}, nil
}

func (f *fakeService) UpdateReview(_ context.Context, in UpdateReviewInput) (*Review, error) {
	f.updated = &in
	if f.err != nil {
		return nil, f.err
	}
	return &Review{ID: in.ReviewID, UserID: in.UserID, Rating: in.Rating, Title: in.Title, Content: in.Content}, nil
}

func (f *fakeService) ListUserReviews(_ context.Context, userID uuid.UUID, _, _ int) ([]*Review, int, error) {
	f.listedFor = userID
	return nil, 0, f.err
}

func (f *fakeService) ListPOIReviews(_ context.Context, poiID uuid.UUID, _, _ int) ([]*Review, int, error) {
	f.listedFor = poiID
	return nil, 0, f.err
}

func (f *fakeService) GetStatistics(_ context.Context, poiID uuid.UUID) (*Statistics, error) {
	f.statsFor = poiID
	if f.err != nil {
		return nil, f.err
	}
	return f.stats, nil
}

func (f *fakeService) LikeReview(_ context.Context, _, _ uuid.UUID, isLike bool) (int, error) {
	f.likedWith = &isLike
	return 3, f.err
}

func newTestHandler(svc Service) *Handler {
	return NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func authed(userID uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, userID.String())
}

func codeOf(t *testing.T, err error) connect.Code {
	t.Helper()
	require.Error(t, err)
	return connect.CodeOf(err)
}

func TestRatingFromProto(t *testing.T) {
	for _, v := range []float64{1, 2, 3, 4, 5} {
		got, err := ratingFromProto(v)
		require.NoError(t, err, "rating %v", v)
		assert.Equal(t, int(v), got)
	}
	for _, v := range []float64{0, 0.5, 4.5, 4.999, 5.5, 6, -1} {
		_, err := ratingFromProto(v)
		assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err), "rating %v", v)
	}
}

func TestCreateReview_RejectsFractionalRating(t *testing.T) {
	svc := &fakeService{}
	h := newTestHandler(svc)
	_, err := h.CreateReview(authed(uuid.New()), connect.NewRequest(&reviewv1.CreateReviewRequest{
		PoiId: uuid.NewString(), Rating: 4.5, Content: "nice",
	}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))
	assert.Nil(t, svc.created, "a fractional rating must not reach the service (it used to be truncated)")
}

func TestCreateReview_ErrorMapping(t *testing.T) {
	cases := map[error]connect.Code{
		ErrAlreadyExists: connect.CodeAlreadyExists,
		ErrPOINotFound:   connect.CodeInvalidArgument,
		errors.New("db"): connect.CodeInternal,
	}
	for svcErr, want := range cases {
		h := newTestHandler(&fakeService{err: svcErr})
		_, err := h.CreateReview(authed(uuid.New()), connect.NewRequest(&reviewv1.CreateReviewRequest{
			PoiId: uuid.NewString(), Rating: 4, Content: "nice",
		}))
		assert.Equal(t, want, codeOf(t, err), "service error %v", svcErr)
	}
}

func TestUpdateReview_UsesCallerNotRequestUserID(t *testing.T) {
	caller := uuid.New()
	svc := &fakeService{}
	h := newTestHandler(svc)
	reviewID := uuid.New()
	visit := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	res, err := h.UpdateReview(authed(caller), connect.NewRequest(&reviewv1.UpdateReviewRequest{
		UserId:    uuid.NewString(), // someone else's id: ignored
		ReviewId:  reviewID.String(),
		Rating:    3,
		Title:     "",
		Content:   "changed my mind",
		PhotoUrls: []string{"https://example.com/a.jpg"},
		VisitDate: timestamppb.New(visit),
	}))
	require.NoError(t, err)
	require.NotNil(t, svc.updated)
	assert.Equal(t, caller, svc.updated.UserID)
	assert.Equal(t, reviewID, svc.updated.ReviewID)
	assert.Equal(t, 3, svc.updated.Rating)
	assert.Equal(t, "changed my mind", svc.updated.Content)
	require.NotNil(t, svc.updated.VisitDate)
	assert.True(t, visit.Equal(*svc.updated.VisitDate))
	assert.Equal(t, "changed my mind", res.Msg.Review.Content)
}

func TestUpdateReview_Errors(t *testing.T) {
	h := newTestHandler(&fakeService{})
	_, err := h.UpdateReview(context.Background(), connect.NewRequest(&reviewv1.UpdateReviewRequest{
		ReviewId: uuid.NewString(), Rating: 3, Content: "x",
	}))
	assert.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))

	_, err = h.UpdateReview(authed(uuid.New()), connect.NewRequest(&reviewv1.UpdateReviewRequest{
		ReviewId: uuid.NewString(), Rating: 2.5, Content: "x",
	}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))

	h = newTestHandler(&fakeService{err: ErrNotFound})
	_, err = h.UpdateReview(authed(uuid.New()), connect.NewRequest(&reviewv1.UpdateReviewRequest{
		ReviewId: uuid.NewString(), Rating: 3, Content: "x",
	}))
	assert.Equal(t, connect.CodeNotFound, codeOf(t, err), "someone else's review is NotFound")
}

func TestGetUserReviews_EmptyUserIDMeansCaller(t *testing.T) {
	caller := uuid.New()
	svc := &fakeService{}
	h := newTestHandler(svc)

	_, err := h.GetUserReviews(authed(caller), connect.NewRequest(&reviewv1.GetUserReviewsRequest{}))
	require.NoError(t, err)
	assert.Equal(t, caller, svc.listedFor)

	other := uuid.New()
	_, err = h.GetUserReviews(authed(caller), connect.NewRequest(&reviewv1.GetUserReviewsRequest{UserId: other.String()}))
	require.NoError(t, err)
	assert.Equal(t, other, svc.listedFor)

	// A malformed id used to fall back silently to the caller's reviews.
	_, err = h.GetUserReviews(authed(caller), connect.NewRequest(&reviewv1.GetUserReviewsRequest{UserId: "not-a-uuid"}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))

	_, err = h.GetUserReviews(context.Background(), connect.NewRequest(&reviewv1.GetUserReviewsRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
}

func TestLikeReview_PassesUnlikeAndMapsNotFound(t *testing.T) {
	svc := &fakeService{}
	h := newTestHandler(svc)
	res, err := h.LikeReview(authed(uuid.New()), connect.NewRequest(&reviewv1.LikeReviewRequest{
		ReviewId: uuid.NewString(), IsLike: false,
	}))
	require.NoError(t, err)
	require.NotNil(t, svc.likedWith)
	assert.False(t, *svc.likedWith)
	assert.EqualValues(t, 3, res.Msg.NewHelpfulCount)

	h = newTestHandler(&fakeService{err: ErrNotFound})
	_, err = h.LikeReview(authed(uuid.New()), connect.NewRequest(&reviewv1.LikeReviewRequest{
		ReviewId: uuid.NewString(), IsLike: true,
	}))
	assert.Equal(t, connect.CodeNotFound, codeOf(t, err))
}

func TestGetReviewStatistics_MapsBreakdown(t *testing.T) {
	poiID := uuid.New()
	last := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc := &fakeService{stats: &Statistics{
		POIID: poiID, TotalReviews: 6, AverageRating: 3.5,
		Distribution: [5]int{1, 0, 2, 1, 2}, LastReviewAt: &last,
	}}
	h := newTestHandler(svc)

	res, err := h.GetReviewStatistics(context.Background(), connect.NewRequest(&reviewv1.GetReviewStatisticsRequest{PoiId: poiID.String()}))
	require.NoError(t, err)
	st := res.Msg.Statistics
	assert.Equal(t, poiID.String(), st.PoiId)
	assert.EqualValues(t, 6, st.TotalReviews)
	assert.InDelta(t, 3.5, st.OverallRating, 1e-9)
	assert.EqualValues(t, 1, st.RatingBreakdown.OneStar)
	assert.EqualValues(t, 0, st.RatingBreakdown.TwoStar)
	assert.EqualValues(t, 2, st.RatingBreakdown.ThreeStar)
	assert.EqualValues(t, 1, st.RatingBreakdown.FourStar)
	assert.EqualValues(t, 2, st.RatingBreakdown.FiveStar)
	assert.True(t, last.Equal(st.LastUpdated.AsTime()))

	_, err = h.GetReviewStatistics(context.Background(), connect.NewRequest(&reviewv1.GetReviewStatisticsRequest{PoiId: "nope"}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))
}

func TestGetContentReviews_POIOnly(t *testing.T) {
	poiID := uuid.New()
	svc := &fakeService{stats: &Statistics{POIID: poiID}}
	h := newTestHandler(svc)

	res, err := h.GetContentReviews(context.Background(), connect.NewRequest(&reviewv1.GetContentReviewsRequest{
		ContentType: reviewv1.ReviewContentType_REVIEW_CONTENT_TYPE_POI, ContentId: poiID.String(),
	}))
	require.NoError(t, err)
	assert.Equal(t, poiID, svc.listedFor)
	assert.Equal(t, poiID, svc.statsFor)
	assert.NotNil(t, res.Msg.Statistics)

	_, err = h.GetContentReviews(context.Background(), connect.NewRequest(&reviewv1.GetContentReviewsRequest{
		ContentType: reviewv1.ReviewContentType_REVIEW_CONTENT_TYPE_HOTEL, ContentId: poiID.String(),
	}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))
}

func TestReportReview_StillUnimplemented(t *testing.T) {
	h := newTestHandler(&fakeService{})
	_, err := h.ReportReview(authed(uuid.New()), connect.NewRequest(&reviewv1.ReportReviewRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, codeOf(t, err))
}

func TestToProtoReview_FillsContentAndMemberSince(t *testing.T) {
	poiID := uuid.New()
	since := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	p := toProtoReview(&Review{ID: uuid.New(), UserID: uuid.New(), POIID: poiID, Rating: 4, ReviewerSince: since})
	assert.Equal(t, poiID.String(), p.ContentId)
	assert.Equal(t, reviewv1.ReviewContentType_REVIEW_CONTENT_TYPE_POI, p.ContentType)
	require.NotNil(t, p.Reviewer.MemberSince)
	assert.True(t, since.Equal(p.Reviewer.MemberSince.AsTime()))
	assert.InDelta(t, 4.0, p.Rating, 0)
}

// Marking your own review helpful is refused, and the refusal is a
// permission problem the client can word, not an internal error.
func TestLikeReview_OwnReviewIsPermissionDenied(t *testing.T) {
	h := newTestHandler(&fakeService{err: ErrOwnReview})
	_, err := h.LikeReview(authed(uuid.New()), connect.NewRequest(&reviewv1.LikeReviewRequest{
		ReviewId: uuid.NewString(), IsLike: true,
	}))
	assert.Equal(t, connect.CodePermissionDenied, codeOf(t, err))
}

// My reviews carries the summary the clients used to compute from the rows.
func TestGetUserReviews_FillsStatistics(t *testing.T) {
	caller := uuid.New()
	svc := &fakeService{userStats: &UserStatistics{TotalReviews: 7, AverageRatingGiven: 4.3, HelpfulVotesReceived: 12}}
	h := newTestHandler(svc)
	res, err := h.GetUserReviews(authed(caller), connect.NewRequest(&reviewv1.GetUserReviewsRequest{}))
	require.NoError(t, err)
	require.NotNil(t, res.Msg.Statistics)
	assert.EqualValues(t, 7, res.Msg.Statistics.TotalReviews)
	assert.InDelta(t, 4.3, res.Msg.Statistics.AverageRatingGiven, 0.001)
	assert.EqualValues(t, 12, res.Msg.Statistics.HelpfulVotesReceived)
	assert.Equal(t, "guide", res.Msg.Statistics.ReviewerLevel)
	assert.Equal(t, caller, svc.statsFor)
}

func TestReviewerLevel(t *testing.T) {
	assert.Equal(t, "new", reviewerLevel(0))
	assert.Equal(t, "explorer", reviewerLevel(1))
	assert.Equal(t, "explorer", reviewerLevel(4))
	assert.Equal(t, "guide", reviewerLevel(5))
	assert.Equal(t, "guide", reviewerLevel(19))
	assert.Equal(t, "expert", reviewerLevel(20))
}
