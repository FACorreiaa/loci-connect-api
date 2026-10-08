package social

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social/socialconnect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements SocialService.
type Handler struct {
	socialconnect.UnimplementedSocialServiceHandler
	svc *Service
	log *slog.Logger
}

// NewHandler wires the handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

func caller(ctx context.Context) (uuid.UUID, error) {
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}
	return id, nil
}

// optionalCaller is the caller, or uuid.Nil when signed out.
func optionalCaller(ctx context.Context) uuid.UUID {
	id, err := caller(ctx)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func (h *Handler) connectErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrSelf):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrLimited):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNotLinked):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		h.log.Error("social rpc failed", slog.Any("error", err))
		return connect.NewError(connect.CodeInternal, errors.New("something went wrong, try again"))
	}
}

func parseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid "+what))
	}
	return id, nil
}

func inviteProto(inv *Invite, inviter *socialv1.PublicUser) *socialv1.Invite {
	out := &socialv1.Invite{
		Code:      inv.Code,
		Url:       InviteURL(inv.Code),
		Inviter:   inviter,
		ShareText: ShareText,
	}
	if inv.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*inv.ExpiresAt)
	}
	return out
}

func (h *Handler) card(ctx context.Context, id uuid.UUID) (*socialv1.PublicUser, error) {
	cards, err := h.svc.repo.PublicUsers(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	if cards[id] == nil {
		return nil, ErrNotFound
	}
	return cards[id], nil
}

func (h *Handler) GetMyInvite(ctx context.Context, _ *connect.Request[socialv1.GetMyInviteRequest]) (*connect.Response[socialv1.GetMyInviteResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := h.svc.MyInvite(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	me, err := h.card(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&socialv1.GetMyInviteResponse{Invite: inviteProto(inv, me)}), nil
}

func (h *Handler) RotateInvite(ctx context.Context, _ *connect.Request[socialv1.RotateInviteRequest]) (*connect.Response[socialv1.GetMyInviteResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := h.svc.RotateInvite(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	me, err := h.card(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&socialv1.GetMyInviteResponse{Invite: inviteProto(inv, me)}), nil
}

func (h *Handler) GetInvite(ctx context.Context, req *connect.Request[socialv1.GetInviteRequest]) (*connect.Response[socialv1.GetInviteResponse], error) {
	inv, err := h.svc.LookupInvite(ctx, req.Msg.GetCode())
	if err != nil {
		return nil, h.connectErr(err)
	}
	inviter, err := h.card(ctx, inv.UserID)
	if err != nil {
		return nil, h.connectErr(err)
	}
	resp := &socialv1.GetInviteResponse{Invite: inviteProto(inv, inviter)}
	if viewer := optionalCaller(ctx); viewer != uuid.Nil {
		rel, blockedBy, err := h.svc.repo.RelationOf(ctx, viewer, inv.UserID)
		if err != nil {
			return nil, h.connectErr(err)
		}
		if blockedBy {
			return nil, h.connectErr(ErrNotFound)
		}
		resp.Relationship = socialv1.Relationship(rel)
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) AcceptInvite(ctx context.Context, req *connect.Request[socialv1.AcceptInviteRequest]) (*connect.Response[socialv1.AcceptInviteResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	inviter, since, err := h.svc.AcceptInvite(ctx, uid, req.Msg.GetCode())
	if err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&socialv1.AcceptInviteResponse{
		Friend: &socialv1.Friend{User: inviter, Since: timestamppb.New(since)},
	}), nil
}

// target resolves a user_id / username oneof.
func (h *Handler) target(ctx context.Context, userID, username string) (uuid.UUID, error) {
	if userID != "" {
		return parseID(userID, "user ID")
	}
	id, err := h.svc.repo.UserIDByUsername(ctx, username)
	if err != nil {
		return uuid.Nil, h.connectErr(err)
	}
	return id, nil
}

func (h *Handler) SendFriendRequest(ctx context.Context, req *connect.Request[socialv1.SendFriendRequestRequest]) (*connect.Response[socialv1.SendFriendRequestResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	to, err := h.target(ctx, req.Msg.GetUserId(), req.Msg.GetUsername())
	if err != nil {
		return nil, err
	}
	rel, reqID, err := h.svc.SendRequest(ctx, uid, to)
	if err != nil {
		return nil, h.connectErr(err)
	}
	resp := &socialv1.SendFriendRequestResponse{Relationship: socialv1.Relationship(rel)}
	if rel == RelationRequested && reqID != uuid.Nil {
		cards, err := h.svc.repo.PublicUsers(ctx, []uuid.UUID{uid, to})
		if err != nil {
			return nil, h.connectErr(err)
		}
		resp.Request = &socialv1.FriendRequest{
			Id: reqID.String(), From: cards[uid], To: cards[to], CreatedAt: timestamppb.Now(),
		}
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) RespondFriendRequest(ctx context.Context, req *connect.Request[socialv1.RespondFriendRequestRequest]) (*connect.Response[socialv1.RespondFriendRequestResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.Msg.GetRequestId(), "request ID")
	if err != nil {
		return nil, err
	}
	friend, err := h.svc.Respond(ctx, uid, id, req.Msg.GetAccept())
	if err != nil {
		return nil, h.connectErr(err)
	}
	resp := &socialv1.RespondFriendRequestResponse{}
	if friend != nil {
		resp.Friend = &socialv1.Friend{User: friend, Since: timestamppb.Now()}
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) CancelFriendRequest(ctx context.Context, req *connect.Request[socialv1.CancelFriendRequestRequest]) (*connect.Response[socialv1.CancelFriendRequestResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.Msg.GetRequestId(), "request ID")
	if err != nil {
		return nil, err
	}
	if err := h.svc.repo.CancelRequest(ctx, id, uid); err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&socialv1.CancelFriendRequestResponse{}), nil
}

func requestProto(r Request) *socialv1.FriendRequest {
	return &socialv1.FriendRequest{Id: r.ID.String(), From: r.From, To: r.To, CreatedAt: timestamppb.New(r.CreatedAt)}
}

func (h *Handler) ListFriendRequests(ctx context.Context, req *connect.Request[socialv1.ListFriendRequestsRequest]) (*connect.Response[socialv1.ListFriendRequestsResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	incoming := req.Msg.GetDirection() != socialv1.RequestDirection_REQUEST_DIRECTION_OUTGOING
	list, err := h.svc.repo.ListRequests(ctx, uid, incoming)
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := make([]*socialv1.FriendRequest, 0, len(list))
	for _, r := range list {
		out = append(out, requestProto(r))
	}
	return connect.NewResponse(&socialv1.ListFriendRequestsResponse{Requests: out}), nil
}

func (h *Handler) ListFriends(ctx context.Context, _ *connect.Request[socialv1.ListFriendsRequest]) (*connect.Response[socialv1.ListFriendsResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	list, err := h.svc.repo.ListFriends(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := make([]*socialv1.Friend, 0, len(list))
	for _, f := range list {
		out = append(out, &socialv1.Friend{User: f.User, Since: timestamppb.New(f.Since)})
	}
	return connect.NewResponse(&socialv1.ListFriendsResponse{Friends: out}), nil
}

func (h *Handler) RemoveFriend(ctx context.Context, req *connect.Request[socialv1.RemoveFriendRequest]) (*connect.Response[socialv1.RemoveFriendResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	other, err := parseID(req.Msg.GetUserId(), "user ID")
	if err != nil {
		return nil, err
	}
	if err := h.svc.repo.RemoveFriend(ctx, uid, other); err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&socialv1.RemoveFriendResponse{}), nil
}

func (h *Handler) BlockUser(ctx context.Context, req *connect.Request[socialv1.BlockUserRequest]) (*connect.Response[socialv1.BlockUserResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	other, err := parseID(req.Msg.GetUserId(), "user ID")
	if err != nil {
		return nil, err
	}
	if err := h.svc.Block(ctx, uid, other); err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&socialv1.BlockUserResponse{}), nil
}

func (h *Handler) UnblockUser(ctx context.Context, req *connect.Request[socialv1.UnblockUserRequest]) (*connect.Response[socialv1.UnblockUserResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	other, err := parseID(req.Msg.GetUserId(), "user ID")
	if err != nil {
		return nil, err
	}
	if err := h.svc.repo.Unblock(ctx, uid, other); err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&socialv1.UnblockUserResponse{}), nil
}

func (h *Handler) MatchContacts(ctx context.Context, req *connect.Request[socialv1.MatchContactsRequest]) (*connect.Response[socialv1.MatchContactsResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	matches, err := h.svc.MatchContacts(ctx, uid, req.Msg.GetHashes())
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := make([]*socialv1.ContactMatch, 0, len(matches))
	for _, m := range matches {
		out = append(out, &socialv1.ContactMatch{Hash: m.Hash, User: m.User, Relationship: socialv1.Relationship(m.Relation)})
	}
	return connect.NewResponse(&socialv1.MatchContactsResponse{Matches: out}), nil
}

func (h *Handler) SearchUsers(ctx context.Context, req *connect.Request[socialv1.SearchUsersRequest]) (*connect.Response[socialv1.SearchUsersResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.TrimPrefix(strings.TrimSpace(req.Msg.GetQuery()), "@")
	if len(q) < 2 {
		return connect.NewResponse(&socialv1.SearchUsersResponse{}), nil
	}
	results, err := h.svc.Search(ctx, uid, q, int(req.Msg.GetLimit()))
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := make([]*socialv1.UserResult, 0, len(results))
	for _, r := range results {
		out = append(out, &socialv1.UserResult{User: r.User, Relationship: socialv1.Relationship(r.Relation)})
	}
	return connect.NewResponse(&socialv1.SearchUsersResponse{Users: out}), nil
}

func (h *Handler) GetPublicProfile(ctx context.Context, req *connect.Request[socialv1.GetPublicProfileRequest]) (*connect.Response[socialv1.GetPublicProfileResponse], error) {
	target, err := h.target(ctx, req.Msg.GetUserId(), req.Msg.GetUsername())
	if err != nil {
		return nil, err
	}
	p, err := h.svc.Profile(ctx, optionalCaller(ctx), target)
	if err != nil {
		return nil, h.connectErr(err)
	}
	resp := &socialv1.GetPublicProfileResponse{
		User: p.User,
		Stats: &socialv1.ProfileStats{
			Cities: p.Stats.Cities, Countries: p.Stats.Countries,
			VisibleTrips: p.Stats.VisibleTrips, Friends: p.Stats.Friends,
		},
		Relationship: socialv1.Relationship(p.Relation),
	}
	if !p.MemberSince.IsZero() {
		resp.MemberSince = timestamppb.New(p.MemberSince.Truncate(24 * time.Hour))
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) MatchFacebookFriends(ctx context.Context, _ *connect.Request[socialv1.MatchFacebookFriendsRequest]) (*connect.Response[socialv1.MatchFacebookFriendsResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	results, err := h.svc.FacebookFriends(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := make([]*socialv1.UserResult, 0, len(results))
	for _, r := range results {
		out = append(out, &socialv1.UserResult{User: r.User, Relationship: socialv1.Relationship(r.Relation)})
	}
	return connect.NewResponse(&socialv1.MatchFacebookFriendsResponse{Matches: out}), nil
}
