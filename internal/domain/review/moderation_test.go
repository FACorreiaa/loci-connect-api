package review

import (
	"context"
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

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type resolveCall struct {
	review, moderator uuid.UUID
	action            ModerationAction
}

func (f *fakeService) ListReportedReviews(_ context.Context, _, _ int) ([]*ReportedReview, int, error) {
	return f.reportedQueue, len(f.reportedQueue), f.err
}

func (f *fakeService) ResolveReport(_ context.Context, reviewID, moderatorID uuid.UUID, action ModerationAction) error {
	f.resolved = &resolveCall{reviewID, moderatorID, action}
	return f.err
}

func withClaims(userID uuid.UUID, email, role string) context.Context {
	return interceptors.ContextWithClaims(context.Background(),
		&interceptors.Claims{UserID: userID.String(), Email: email, Role: role})
}

// Only moderators reach the queue: a token with role "admin", or an account
// on the admin list (ADMIN_EMAIL), matched case-insensitively. Everyone else
// is PermissionDenied; no token at all is Unauthenticated.
func TestModerationRPCs_AdminOnly(t *testing.T) {
	h := newTestHandler(&fakeService{}).WithAdmins(" Boss@Loci.app ", "")
	list := connect.NewRequest(&reviewv1.ListReportedReviewsRequest{})
	resolve := connect.NewRequest(&reviewv1.ResolveReviewReportRequest{
		ReviewId: uuid.NewString(), Action: reviewv1.ReviewModerationAction_REVIEW_MODERATION_ACTION_KEEP,
	})

	_, err := h.ListReportedReviews(context.Background(), list)
	assert.Equal(t, connect.CodeUnauthenticated, codeOf(t, err))
	_, err = h.ListReportedReviews(authed(uuid.New()), list)
	assert.Equal(t, connect.CodeUnauthenticated, codeOf(t, err), "a user id without claims is not enough")

	member := withClaims(uuid.New(), "someone@loci.app", "member")
	_, err = h.ListReportedReviews(member, list)
	assert.Equal(t, connect.CodePermissionDenied, codeOf(t, err))
	_, err = h.ResolveReviewReport(member, resolve)
	assert.Equal(t, connect.CodePermissionDenied, codeOf(t, err))

	_, err = h.ListReportedReviews(withClaims(uuid.New(), "boss@loci.app", "member"), list)
	require.NoError(t, err, "an ADMIN_EMAIL account moderates")
	_, err = h.ListReportedReviews(withClaims(uuid.New(), "", "admin"), list)
	require.NoError(t, err, "role admin moderates")

	// An empty email never matches an empty entry.
	_, err = h.ListReportedReviews(withClaims(uuid.New(), "", "member"), list)
	assert.Equal(t, connect.CodePermissionDenied, codeOf(t, err))

	// Without WithAdmins only role admin gets in.
	_, err = newTestHandler(&fakeService{}).ListReportedReviews(withClaims(uuid.New(), "boss@loci.app", "member"), list)
	assert.Equal(t, connect.CodePermissionDenied, codeOf(t, err))
}

func TestResolveReviewReport_MapsActionAndModerator(t *testing.T) {
	svc := &fakeService{}
	h := newTestHandler(svc)
	admin, reviewID := uuid.New(), uuid.New()
	ctx := withClaims(admin, "", "admin")

	_, err := h.ResolveReviewReport(ctx, connect.NewRequest(&reviewv1.ResolveReviewReportRequest{
		ReviewId: reviewID.String(), Action: reviewv1.ReviewModerationAction_REVIEW_MODERATION_ACTION_REMOVE,
	}))
	require.NoError(t, err)
	assert.Equal(t, &resolveCall{reviewID, admin, ModerationRemove}, svc.resolved)

	_, err = h.ResolveReviewReport(ctx, connect.NewRequest(&reviewv1.ResolveReviewReportRequest{
		ReviewId: reviewID.String(), Action: reviewv1.ReviewModerationAction_REVIEW_MODERATION_ACTION_KEEP,
	}))
	require.NoError(t, err)
	assert.Equal(t, ModerationKeep, svc.resolved.action)

	_, err = h.ResolveReviewReport(ctx, connect.NewRequest(&reviewv1.ResolveReviewReportRequest{ReviewId: reviewID.String()}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))
	_, err = h.ResolveReviewReport(ctx, connect.NewRequest(&reviewv1.ResolveReviewReportRequest{
		ReviewId: "nope", Action: reviewv1.ReviewModerationAction_REVIEW_MODERATION_ACTION_KEEP,
	}))
	assert.Equal(t, connect.CodeInvalidArgument, codeOf(t, err))

	svc.err = ErrNotFound
	_, err = h.ResolveReviewReport(ctx, connect.NewRequest(&reviewv1.ResolveReviewReportRequest{
		ReviewId: reviewID.String(), Action: reviewv1.ReviewModerationAction_REVIEW_MODERATION_ACTION_KEEP,
	}))
	assert.Equal(t, connect.CodeNotFound, codeOf(t, err))

	// The contract refuses UNSPECIFIED before the handler runs.
	require.Error(t, protovalidate.Validate(&reviewv1.ResolveReviewReportRequest{ReviewId: reviewID.String()}))
	require.NoError(t, protovalidate.Validate(&reviewv1.ListReportedReviewsRequest{}))
}

func TestListReportedReviews_MapsTheQueue(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	r := &Review{ID: uuid.New(), UserID: uuid.New(), Rating: 1, Content: "x", Hidden: true, ReportCount: 4}
	svc := &fakeService{reportedQueue: []*ReportedReview{{
		Review: r, OpenReports: 3, Reasons: map[string]int{"fake": 1, "spam": 2, "other": 1},
		Details: []string{"newest", "older"}, LastReportedAt: at,
	}}}
	resp, err := newTestHandler(svc).ListReportedReviews(withClaims(uuid.New(), "", "admin"),
		connect.NewRequest(&reviewv1.ListReportedReviewsRequest{Limit: 5, Page: 1}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetReviews(), 1)
	got := resp.Msg.GetReviews()[0]
	assert.Equal(t, r.ID.String(), got.GetReview().GetId())
	assert.True(t, got.GetReview().GetHidden())
	assert.True(t, got.GetHidden())
	assert.Equal(t, int32(3), got.GetReportCount(), "open reports, not the all-time count")
	assert.Equal(t, []string{"newest", "older"}, got.GetDetails())
	assert.Equal(t, at, got.GetLastReportedAt().AsTime())
	var reasons []string
	for _, rc := range got.GetReasons() {
		reasons = append(reasons, rc.GetReason())
	}
	assert.Equal(t, []string{"spam", "fake", "other"}, reasons, "most-given first, ties alphabetical")
	assert.Equal(t, int32(2), got.GetReasons()[0].GetCount())
	assert.Equal(t, int32(5), resp.Msg.GetPagination().GetPageSize())
}

// GetReview of a hidden review is NotFound for anyone but its author.
func TestService_GetReview_HiddenOnlyForTheAuthor(t *testing.T) {
	author := uuid.New()
	hidden := &Review{ID: uuid.New(), UserID: author, Hidden: true}
	svc := NewService(&fakeRepo{reviews: map[uuid.UUID]*Review{hidden.ID: hidden}}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := svc.GetReview(context.Background(), hidden.ID, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetReview(context.Background(), hidden.ID, uuid.Nil)
	require.ErrorIs(t, err, ErrNotFound)
	got, err := svc.GetReview(context.Background(), hidden.ID, author)
	require.NoError(t, err)
	assert.Same(t, hidden, got)
}

func TestToProtoReview_CarriesHidden(t *testing.T) {
	assert.True(t, toProtoReview(&Review{Hidden: true}).GetHidden())
	assert.False(t, toProtoReview(&Review{}).GetHidden())
}
