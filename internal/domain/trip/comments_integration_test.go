//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"connectrpc.com/connect"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/social"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type commentPush struct {
	to   []uuid.UUID
	from string
	body string
}

type recordingCommentNotifier struct {
	mu   sync.Mutex
	sent []commentPush
}

func (r *recordingCommentNotifier) TripCommented(_ context.Context, to []uuid.UUID, _ uuid.UUID, _ string, from *socialv1.PublicUser, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, commentPush{to: to, from: from.GetId(), body: body})
}

func (r *recordingCommentNotifier) last() commentPush {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[len(r.sent)-1]
}

// Trip comments on the real schema: everyone on the trip writes and reads
// them, strangers do not; the owner and the author delete; a block hides an
// author's comments both ways; a report hides a comment from its reporter at
// once and three hide it from everyone until an admin keeps or removes it.
func TestTripCommentsEndToEnd(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, log)
	socialSvc := social.NewService(social.NewRepository(testTripDB), nil, log)
	push := &recordingCommentNotifier{}
	const adminEmail = "trip-comments-admin@loci.test"
	h := NewHandler(repo, "https://api.example", nil, nil).
		WithSharing(NewSharingRepository(testTripDB, log), socialSvc).
		WithMembers(NewMembersRepository(testTripDB, log), socialSvc).
		WithComments(NewCommentsRepository(testTripDB), push, " Trip-Comments-Admin@loci.test ")
	ctx := context.Background()

	user := func(name string) uuid.UUID {
		return newTripUser(t, "comments-"+name+"-"+uuid.NewString()+"@loci.test")
	}
	ana, rui, vic, pat, liz, sam, eve := user("ana"), user("rui"), user("vic"), user("pat"), user("liz"), user("sam"), user("eve")
	trip, err := repo.SaveTrip(ctx, &Trip{
		UserID: ana, CityName: "Lisbon", Title: "Lisbon long weekend",
		Days: []TripDay{{DayNumber: 1, Stops: []TripStop{{Name: "Miradouro da Graça"}}}},
	}, 0)
	require.NoError(t, err)
	stopID := trip.Days[0].Stops[0].ID
	for id, role := range map[uuid.UUID]string{rui: "co_planner", vic: "viewer", pat: "viewer", liz: "viewer", sam: "viewer"} {
		_, err := testTripDB.Exec(ctx, `INSERT INTO trip_members (trip_id, user_id, role, invited_by) VALUES ($1, $2, $3, $4)`, trip.ID, id, role, ana)
		require.NoError(t, err)
	}

	add := func(who uuid.UUID, stop, body string) (*tripv1.TripComment, error) {
		resp, err := h.AddTripComment(as(who), connect.NewRequest(&tripv1.AddTripCommentRequest{TripId: trip.ID.String(), StopId: stop, Body: body}))
		if err != nil {
			return nil, err
		}
		return resp.Msg, nil
	}
	list := func(who uuid.UUID) []string {
		resp, err := h.ListTripComments(as(who), connect.NewRequest(&tripv1.ListTripCommentsRequest{TripId: trip.ID.String()}))
		require.NoError(t, err)
		var bodies []string
		for _, c := range resp.Msg.GetComments() {
			bodies = append(bodies, c.GetBody())
		}
		return bodies
	}
	report := func(who uuid.UUID, id string) error {
		_, err := h.ReportTripComment(as(who), connect.NewRequest(&tripv1.ReportTripCommentRequest{CommentId: id, Reason: "Spam"}))
		return err
	}

	// A stranger can neither write nor read.
	_, err = add(eve, "", "hello")
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = h.ListTripComments(as(eve), connect.NewRequest(&tripv1.ListTripCommentsRequest{TripId: trip.ID.String()}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// Members write about the trip and about a stop that is on it.
	fromRui, err := add(rui, "", "  Shall we skip the castle?  ")
	require.NoError(t, err)
	assert.Equal(t, "Shall we skip the castle?", fromRui.GetBody())
	assert.True(t, fromRui.GetMine())
	assert.Equal(t, rui.String(), fromRui.GetAuthor().GetId())
	assert.ElementsMatch(t, []uuid.UUID{ana, vic, pat, liz, sam}, push.last().to, "everyone else on the trip hears about it")

	fromVic, err := add(vic, stopID.String(), "Best at sunset")
	require.NoError(t, err)
	assert.Equal(t, stopID.String(), fromVic.GetStopId())
	_, err = add(vic, uuid.NewString(), "about nothing")
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "the stop must be on the trip")

	// Reading order is oldest first; the owner may delete anything.
	resp, err := h.ListTripComments(as(ana), connect.NewRequest(&tripv1.ListTripCommentsRequest{TripId: trip.ID.String()}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetComments(), 2)
	first := resp.Msg.GetComments()[0]
	assert.Equal(t, fromRui.GetId(), first.GetId())
	assert.False(t, first.GetMine())
	assert.True(t, first.GetCanDelete())
	assert.False(t, resp.Msg.GetHasMore())

	page, err := h.ListTripComments(as(ana), connect.NewRequest(&tripv1.ListTripCommentsRequest{TripId: trip.ID.String(), Limit: 1}))
	require.NoError(t, err)
	require.Len(t, page.Msg.GetComments(), 1)
	assert.Equal(t, fromVic.GetId(), page.Msg.GetComments()[0].GetId(), "the newest page first")
	assert.True(t, page.Msg.GetHasMore())

	// Someone else's comment is not a viewer's to delete, nor your own to report.
	_, err = h.DeleteTripComment(as(vic), connect.NewRequest(&tripv1.DeleteTripCommentRequest{CommentId: fromRui.GetId()}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(report(rui, fromRui.GetId())))
	_, err = h.ReportTripComment(as(vic), connect.NewRequest(&tripv1.ReportTripCommentRequest{CommentId: fromRui.GetId(), Reason: "rude"}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// A block hides each side's comments from the other, and stops the push.
	require.NoError(t, socialSvc.Block(ctx, vic, rui))
	assert.Equal(t, []string{"Best at sunset"}, list(vic))
	assert.Equal(t, []string{"Shall we skip the castle?"}, list(rui), "and the other way round")
	_, err = add(rui, "", "Or the tram?")
	require.NoError(t, err)
	assert.NotContains(t, push.last().to, vic)

	// One report hides a comment from its reporter; three, from everyone.
	require.NoError(t, report(ana, fromVic.GetId()))
	assert.NotContains(t, list(ana), "Best at sunset")
	assert.Contains(t, list(pat), "Best at sunset")
	require.NoError(t, report(pat, fromVic.GetId()))
	require.NoError(t, report(pat, fromVic.GetId()), "reporting again replaces the report")
	require.NoError(t, report(liz, fromVic.GetId()))
	assert.NotContains(t, list(sam), "Best at sunset", "three reporters hide it")
	assert.Equal(t, []string{"Best at sunset"}, list(vic), "its author still sees it")

	// Moderation is for admins only.
	_, err = h.ListReportedTripComments(as(ana), connect.NewRequest(&tripv1.ListReportedTripCommentsRequest{}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	admin := interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: eve.String(), Email: adminEmail})
	queue, err := h.ListReportedTripComments(admin, connect.NewRequest(&tripv1.ListReportedTripCommentsRequest{}))
	require.NoError(t, err)
	var reported *tripv1.ReportedTripComment
	for _, rc := range queue.Msg.GetComments() {
		if rc.GetComment().GetId() == fromVic.GetId() {
			reported = rc
		}
	}
	require.NotNil(t, reported)
	assert.Equal(t, int32(3), reported.GetReportCount())
	assert.True(t, reported.GetHidden())
	require.Len(t, reported.GetReasons(), 1)
	assert.Equal(t, "spam", reported.GetReasons()[0].GetReason())

	resolve := func(action tripv1.TripCommentModerationAction) {
		_, err := h.ResolveTripCommentReport(admin, connect.NewRequest(&tripv1.ResolveTripCommentReportRequest{CommentId: fromVic.GetId(), Action: action}))
		require.NoError(t, err)
	}
	resolve(tripv1.TripCommentModerationAction_TRIP_COMMENT_MODERATION_ACTION_KEEP)
	assert.Contains(t, list(sam), "Best at sunset", "kept: back for everyone")
	assert.NotContains(t, list(ana), "Best at sunset", "except whoever reported it")
	resolve(tripv1.TripCommentModerationAction_TRIP_COMMENT_MODERATION_ACTION_REMOVE)
	assert.Empty(t, list(vic), "removed: gone, for its author too")
	err = report(pat, fromVic.GetId())
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// The owner deletes a member's comment.
	_, err = h.DeleteTripComment(as(ana), connect.NewRequest(&tripv1.DeleteTripCommentRequest{CommentId: fromRui.GetId()}))
	require.NoError(t, err)
	assert.Equal(t, []string{"Or the tram?"}, list(ana))

	// Sixty an hour.
	_, err = testTripDB.Exec(ctx, `
		INSERT INTO trip_comments (trip_id, author_id, body, created_at)
		SELECT $1, $2, 'flood', NOW() - INTERVAL '1 minute' FROM generate_series(1, 60)`, trip.ID, liz)
	require.NoError(t, err)
	_, err = add(liz, "", "one more")
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
}
