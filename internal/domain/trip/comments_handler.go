package trip

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// CommentNotifier tells a trip's people about a new comment. The push
// notifier implements it.
type CommentNotifier interface {
	TripCommented(ctx context.Context, to []uuid.UUID, tripID uuid.UUID, tripTitle string, from *socialv1.PublicUser, body string)
}

// WithComments attaches trip comments. It needs WithMembers. admins are the
// emails, besides the admin role, that may moderate reported comments.
// Without it the comment RPCs answer Unimplemented.
func (h *Handler) WithComments(repo CommentsRepository, notify CommentNotifier, admins ...string) *Handler {
	h.comments = repo
	h.commentNotify = notify
	h.commentAdmins = make(map[string]struct{}, len(admins))
	for _, e := range admins {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			h.commentAdmins[e] = struct{}{}
		}
	}
	return h
}

func (h *Handler) commentsReady() error {
	if h.comments == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("trip comments are not enabled"))
	}
	return h.membersReady()
}

// commentByID loads a comment and checks the caller is on its trip. Both a
// missing comment and someone else's trip are NotFound.
func (h *Handler) commentByID(ctx context.Context, viewer uuid.UUID, raw string) (*Comment, *Trip, Role, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, nil, RoleNone, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid comment ID"))
	}
	c, err := h.comments.Comment(ctx, id)
	if err != nil {
		return nil, nil, RoleNone, toConnectErr(err)
	}
	t, role, err := h.onTrip(ctx, viewer, c.TripID)
	if err != nil {
		return nil, nil, RoleNone, err
	}
	return c, t, role, nil
}

func (h *Handler) AddTripComment(ctx context.Context, req *connect.Request[tripv1.AddTripCommentRequest]) (*connect.Response[tripv1.TripComment], error) {
	if err := h.commentsReady(); err != nil {
		return nil, err
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseTripID(req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(req.Msg.GetBody())
	if body == "" || len([]rune(body)) > maxCommentBody {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a comment is 1 to 2000 characters"))
	}
	t, role, err := h.onTrip(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	c := &Comment{TripID: id, AuthorID: uid, Body: body}
	if raw := req.Msg.GetStopId(); raw != "" {
		stopID, err := uuid.Parse(raw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid stop ID"))
		}
		if s, _ := FindStop(t, stopID); s == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("that stop is not on the trip"))
		}
		c.StopID = &stopID
	}
	n, err := h.comments.CommentsSince(ctx, uid, h.clock().Add(-time.Hour))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if n >= commentsPerHour {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many comments; try again later"))
	}
	if err := h.comments.AddComment(ctx, c); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	author := h.owner(ctx, uid)
	h.tellAboutComment(ctx, t, author, c)
	h.analytics.Capture(uid.String(), "trip_comment_posted", map[string]any{
		"trip_id": id.String(),
		"on_stop": c.StopID != nil,
		"role":    roleColumn(role),
	})
	return connect.NewResponse(commentToProto(c, author, uid, role)), nil
}

// tellAboutComment pushes a new comment to everyone else on the trip, except
// people the author blocked or who blocked the author. Best effort.
func (h *Handler) tellAboutComment(ctx context.Context, t *Trip, author *socialv1.PublicUser, c *Comment) {
	if h.commentNotify == nil {
		return
	}
	everyone, err := NewMemberAccess(h.sharing, h.members, h.graph).Everyone(ctx, t)
	if err != nil {
		h.logWarn("trip comment recipients", err)
		return
	}
	to := make([]uuid.UUID, 0, len(everyone))
	for _, id := range everyone {
		if id == c.AuthorID {
			continue
		}
		if _, blocked, err := h.graph.Relation(ctx, c.AuthorID, id); err != nil || blocked {
			continue
		}
		to = append(to, id)
	}
	if len(to) > 0 {
		h.commentNotify.TripCommented(ctx, to, t.ID, tripTitle(t), author, c.Body)
	}
}

func (h *Handler) ListTripComments(ctx context.Context, req *connect.Request[tripv1.ListTripCommentsRequest]) (*connect.Response[tripv1.ListTripCommentsResponse], error) {
	if err := h.commentsReady(); err != nil {
		return nil, err
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseTripID(req.Msg.GetTripId())
	if err != nil {
		return nil, err
	}
	_, role, err := h.onTrip(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = defaultCommentsPage
	}
	limit = min(limit, maxCommentsPage)
	var before *time.Time
	if req.Msg.Before != nil {
		b := req.Msg.GetBefore().AsTime()
		before = &b
	}
	rows, err := h.comments.Comments(ctx, id, uid, before, limit+1)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	cards := h.authorCards(ctx, rows)
	out := make([]*tripv1.TripComment, len(rows))
	for i := range rows {
		// Newest first from the store; reading order is oldest first.
		c := &rows[len(rows)-1-i]
		out[i] = commentToProto(c, cards[c.AuthorID], uid, role)
	}
	return connect.NewResponse(&tripv1.ListTripCommentsResponse{Comments: out, HasMore: more}), nil
}

func (h *Handler) authorCards(ctx context.Context, rows []Comment) map[uuid.UUID]*socialv1.PublicUser {
	if len(rows) == 0 {
		return nil
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, c := range rows {
		if !seen[c.AuthorID] {
			seen[c.AuthorID] = true
			ids = append(ids, c.AuthorID)
		}
	}
	cards, err := h.graph.PublicUsers(ctx, ids)
	if err != nil {
		h.logWarn("load comment authors", err)
		return nil
	}
	return cards
}

func (h *Handler) DeleteTripComment(ctx context.Context, req *connect.Request[tripv1.DeleteTripCommentRequest]) (*connect.Response[tripv1.DeleteTripCommentResponse], error) {
	if err := h.commentsReady(); err != nil {
		return nil, err
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	c, _, role, err := h.commentByID(ctx, uid, req.Msg.GetCommentId())
	if err != nil {
		return nil, err
	}
	if c.AuthorID != uid && role != RoleOwner {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only its author or the trip's owner deletes a comment"))
	}
	if err := h.comments.DeleteComment(ctx, c.ID, uid, h.clock()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&tripv1.DeleteTripCommentResponse{}), nil
}

func (h *Handler) ReportTripComment(ctx context.Context, req *connect.Request[tripv1.ReportTripCommentRequest]) (*connect.Response[tripv1.ReportTripCommentResponse], error) {
	if err := h.commentsReady(); err != nil {
		return nil, err
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	reason := strings.ToLower(strings.TrimSpace(req.Msg.GetReason()))
	if !commentReportReasons[reason] {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("reason is spam, inappropriate, offensive or other"))
	}
	c, _, _, err := h.commentByID(ctx, uid, req.Msg.GetCommentId())
	if err != nil {
		return nil, err
	}
	if c.AuthorID == uid {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("you cannot report your own comment"))
	}
	if err := h.comments.ReportComment(ctx, c.ID, uid, reason, strings.TrimSpace(req.Msg.GetDetails())); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	h.analytics.Capture(uid.String(), "trip_comment_reported", map[string]any{
		"trip_id": c.TripID.String(),
		"reason":  reason,
	})
	return connect.NewResponse(&tripv1.ReportTripCommentResponse{}), nil
}

// requireCommentAdmin returns the moderator's id, or the Connect error to
// answer with, as review moderation decides it.
func (h *Handler) requireCommentAdmin(ctx context.Context) (uuid.UUID, error) {
	id, err := userID(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if claims, err := interceptors.GetClaimsFromContext(ctx); err == nil {
		if claims.Role == "admin" {
			return id, nil
		}
		if email := strings.ToLower(strings.TrimSpace(claims.Email)); email != "" {
			if _, ok := h.commentAdmins[email]; ok {
				return id, nil
			}
		}
	}
	return uuid.Nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin role required"))
}

const (
	defaultReportedPage = 20
	maxReportedPage     = 100
)

func (h *Handler) ListReportedTripComments(ctx context.Context, req *connect.Request[tripv1.ListReportedTripCommentsRequest]) (*connect.Response[tripv1.ListReportedTripCommentsResponse], error) {
	if err := h.commentsReady(); err != nil {
		return nil, err
	}
	admin, err := h.requireCommentAdmin(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = defaultReportedPage
	}
	limit = min(limit, maxReportedPage)
	page := max(int(req.Msg.GetPage()), 1)
	rows, more, err := h.comments.ReportedComments(ctx, limit, (page-1)*limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	plain := make([]Comment, len(rows))
	for i := range rows {
		plain[i] = rows[i].Comment
	}
	cards := h.authorCards(ctx, plain)
	out := make([]*tripv1.ReportedTripComment, len(rows))
	for i := range rows {
		rc := &rows[i]
		reasons := make([]*tripv1.TripCommentReportReason, 0, len(rc.Reasons))
		for _, r := range []string{"spam", "inappropriate", "offensive", "other"} {
			if n := rc.Reasons[r]; n > 0 {
				reasons = append(reasons, &tripv1.TripCommentReportReason{Reason: r, Count: int32(n)})
			}
		}
		out[i] = &tripv1.ReportedTripComment{
			Comment:        commentToProto(&rc.Comment, cards[rc.AuthorID], admin, RoleNone),
			ReportCount:    int32(rc.Reporters),
			Reasons:        reasons,
			Details:        rc.Details,
			LastReportedAt: timestamppb.New(rc.LastReported),
			Hidden:         rc.Hidden,
		}
	}
	return connect.NewResponse(&tripv1.ListReportedTripCommentsResponse{Comments: out, HasMore: more}), nil
}

func (h *Handler) ResolveTripCommentReport(ctx context.Context, req *connect.Request[tripv1.ResolveTripCommentReportRequest]) (*connect.Response[tripv1.ResolveTripCommentReportResponse], error) {
	if err := h.commentsReady(); err != nil {
		return nil, err
	}
	admin, err := h.requireCommentAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetCommentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid comment ID"))
	}
	var decision string
	switch req.Msg.GetAction() {
	case tripv1.TripCommentModerationAction_TRIP_COMMENT_MODERATION_ACTION_KEEP:
		decision = ModerationKeep
	case tripv1.TripCommentModerationAction_TRIP_COMMENT_MODERATION_ACTION_REMOVE:
		decision = ModerationRemove
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("action is keep or remove"))
	}
	if err := h.comments.ResolveComment(ctx, id, admin, decision, h.clock()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&tripv1.ResolveTripCommentReportResponse{}), nil
}

func commentToProto(c *Comment, author *socialv1.PublicUser, viewer uuid.UUID, role Role) *tripv1.TripComment {
	p := &tripv1.TripComment{
		Id:        c.ID.String(),
		TripId:    c.TripID.String(),
		Author:    author,
		Body:      c.Body,
		CreatedAt: timestamppb.New(c.Created),
		Mine:      c.AuthorID == viewer,
		CanDelete: c.AuthorID == viewer || role == RoleOwner,
	}
	if c.StopID != nil {
		p.StopId = c.StopID.String()
	}
	return p
}

// tripTitle names a trip in a notification.
func tripTitle(t *Trip) string {
	switch {
	case t.Title != "":
		return t.Title
	case t.CityName != "":
		return t.CityName + " trip"
	default:
		return "your trip"
	}
}
