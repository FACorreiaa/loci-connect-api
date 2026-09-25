package review

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"buf.build/go/protovalidate"
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

	list       []*Review
	votedBy    map[uuid.UUID]bool // review ids the viewer has voted
	viewerSeen uuid.UUID
	myReview   *Review
	reported   *reportCall
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

type reportCall struct {
	reporter, review uuid.UUID
	reason, details  string
}

func (f *fakeService) GetMyPOIReview(_ context.Context, userID, poiID uuid.UUID) (*Review, error) {
	f.listedFor = poiID
	f.viewerSeen = userID
	if f.err != nil {
		return nil, f.err
	}
	if f.myReview == nil {
		return nil, ErrNotFound
	}
	return f.myReview, nil
}

func (f *fakeService) MarkVotedBy(_ context.Context, viewer uuid.UUID, reviews ...*Review) error {
	f.viewerSeen = viewer
	if viewer == uuid.Nil {
		return nil
	}
	for _, r := range reviews {
		r.VotedByMe = f.votedBy[r.ID]
	}
	return nil
}

func (f *fakeService) ReportReview(_ context.Context, reporterID, reviewID uuid.UUID, reason, details string) error {
	f.reported = &reportCall{reporterID, reviewID, reason, details}
	return f.err
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
	return f.list, len(f.list), f.err
}

func (f *fakeService) ListPOIReviews(_ context.Context, poiID uuid.UUID, _, _ int) ([]*Review, int, error) {
	f.listedFor = poiID
	return f.list, len(f.list), f.err
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

func TestReportReview(t *testing.T) {
	caller, reviewID := uuid.New(), uuid.New()
	svc := &fakeService{}
	h := newTestHandler(svc)
	_, err := h.ReportReview(authed(caller), connect.NewRequest(&reviewv1.ReportReviewRequest{
		UserId: uuid.NewString(), ReviewId: reviewID.String(), Reason: "spam", Details: "link farm",
	}))
	require.NoError(t, err)
	require.NotNil(t, svc.reported)
	assert.Equal(t, reportCall{caller, reviewID, "spam", "link farm"}, *svc.reported,
		"the reporter is the caller, never the request's user_id")

	_, err = h.ReportReview(context.Background(), connect.NewRequest(&reviewv1.ReportReviewRequest{ReviewId: reviewID.String(), Reason: "spam"}))
	assert.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
	_, err = h.ReportReview(authed(caller), connect.NewRequest(&reviewv1.ReportReviewRequest{ReviewId: "nope", Reason: "spam"}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))

	for svcErr, want := range map[error]connect.Code{
		ErrReportOwnReview: connect.CodePermissionDenied,
		ErrNotFound:        connect.CodeNotFound,
		ErrInvalidReview:   connect.CodeInvalidArgument,
	} {
		h := newTestHandler(&fakeService{err: svcErr})
		_, err := h.ReportReview(authed(caller), connect.NewRequest(&reviewv1.ReportReviewRequest{ReviewId: reviewID.String(), Reason: "spam"}))
		assert.Equal(t, want, codeOf(t, err), "service error %v", svcErr)
	}
}

func TestGetPOIReviews_FillsVotedByMeForCaller(t *testing.T) {
	voted, notVoted := &Review{ID: uuid.New()}, &Review{ID: uuid.New()}
	caller := uuid.New()
	svc := &fakeService{list: []*Review{voted, notVoted}, votedBy: map[uuid.UUID]bool{voted.ID: true}}
	h := newTestHandler(svc)

	res, err := h.GetPOIReviews(authed(caller), connect.NewRequest(&reviewv1.GetPOIReviewsRequest{PoiId: uuid.NewString()}))
	require.NoError(t, err)
	assert.Equal(t, caller, svc.viewerSeen)
	require.Len(t, res.Msg.Reviews, 2)
	assert.True(t, res.Msg.Reviews[0].VotedByMe)
	assert.False(t, res.Msg.Reviews[1].VotedByMe)

	// Anonymous reads carry no vote state.
	voted.VotedByMe = false
	res, err = h.GetPOIReviews(context.Background(), connect.NewRequest(&reviewv1.GetPOIReviewsRequest{PoiId: uuid.NewString()}))
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, svc.viewerSeen)
	assert.False(t, res.Msg.Reviews[0].VotedByMe)
}

func TestGetMyPOIReview(t *testing.T) {
	caller, poiID := uuid.New(), uuid.New()
	mine := &Review{ID: uuid.New(), UserID: caller, POIID: poiID, Rating: 4, Content: "ok"}
	svc := &fakeService{myReview: mine}
	h := newTestHandler(svc)

	res, err := h.GetMyPOIReview(authed(caller), connect.NewRequest(&reviewv1.GetMyPOIReviewRequest{PoiId: poiID.String()}))
	require.NoError(t, err)
	assert.Equal(t, mine.ID.String(), res.Msg.Review.Id)
	assert.Equal(t, caller, svc.viewerSeen)
	assert.Equal(t, poiID, svc.listedFor)

	h = newTestHandler(&fakeService{})
	_, err = h.GetMyPOIReview(authed(caller), connect.NewRequest(&reviewv1.GetMyPOIReviewRequest{PoiId: poiID.String()}))
	assert.Equal(t, connect.CodeNotFound, codeOf(t, err), "no review yet is NotFound")

	_, err = h.GetMyPOIReview(context.Background(), connect.NewRequest(&reviewv1.GetMyPOIReviewRequest{PoiId: poiID.String()}))
	assert.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
	_, err = h.GetMyPOIReview(authed(caller), connect.NewRequest(&reviewv1.GetMyPOIReviewRequest{PoiId: "x"}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))
}

func TestNormalizeReportReason(t *testing.T) {
	for in, want := range map[string]string{"spam": "spam", " Fake ": "fake", "OTHER": "other", "offensive": "offensive", "inappropriate": "inappropriate"} {
		got, err := normalizeReportReason(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got)
	}
	for _, in := range []string{"", "rude", "spam!"} {
		_, err := normalizeReportReason(in)
		require.ErrorIs(t, err, ErrInvalidReview, in)
	}
}

type fakeVoteRepo struct {
	Repository
	voted map[uuid.UUID]bool
	calls int
}

func (f *fakeVoteRepo) VotedBy(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (map[uuid.UUID]bool, error) {
	f.calls++
	return f.voted, nil
}

func TestService_MarkVotedBy(t *testing.T) {
	a, b := &Review{ID: uuid.New()}, &Review{ID: uuid.New()}
	repo := &fakeVoteRepo{voted: map[uuid.UUID]bool{b.ID: true}}
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.NoError(t, svc.MarkVotedBy(context.Background(), uuid.Nil, a, b))
	assert.Zero(t, repo.calls, "an anonymous viewer never queries votes")

	require.NoError(t, svc.MarkVotedBy(context.Background(), uuid.New(), a, nil, b))
	assert.False(t, a.VotedByMe)
	assert.True(t, b.VotedByMe)
}

func TestGetUserReviews_FillsRatingDistribution(t *testing.T) {
	svc := &fakeService{userStats: &UserStatistics{TotalReviews: 3, AverageRatingGiven: 4, Distribution: [5]int{0, 0, 1, 1, 1}}}
	h := newTestHandler(svc)
	res, err := h.GetUserReviews(authed(uuid.New()), connect.NewRequest(&reviewv1.GetUserReviewsRequest{}))
	require.NoError(t, err)
	d := res.Msg.Statistics.GetRatingDistribution()
	require.NotNil(t, d)
	assert.EqualValues(t, 0, d.OneStar)
	assert.EqualValues(t, 1, d.ThreeStar)
	assert.EqualValues(t, 1, d.FourStar)
	assert.EqualValues(t, 1, d.FiveStar)
	require.NoError(t, protovalidate.Validate(res.Msg.Statistics))
}

// ReportReview's user_id was min_len 1 although the server acts as the caller;
// GetMyPOIReview needs a poi_id.
func TestReviewContracts(t *testing.T) {
	require.NoError(t, protovalidate.Validate(&reviewv1.ReportReviewRequest{ReviewId: uuid.NewString(), Reason: "spam"}))
	require.Error(t, protovalidate.Validate(&reviewv1.ReportReviewRequest{ReviewId: uuid.NewString()}), "a reason is still required")
	require.NoError(t, protovalidate.Validate(&reviewv1.GetMyPOIReviewRequest{PoiId: uuid.NewString()}))
	require.Error(t, protovalidate.Validate(&reviewv1.GetMyPOIReviewRequest{}))
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

	p = toProtoReview(&Review{ID: uuid.New(), VotedByMe: true, ReportCount: 2})
	assert.True(t, p.VotedByMe)
	assert.EqualValues(t, 2, p.ReportCount)
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
