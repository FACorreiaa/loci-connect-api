package trip

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Befriender makes two people friends, the way accepting a friend invite
// does. The social service implements it; trip does not import social.
type Befriender interface {
	Befriend(ctx context.Context, a, b uuid.UUID) error
}

// WithMembers attaches planning together. It needs WithSharing too. Without it
// the member RPCs answer Unimplemented and GetTrip serves owners only.
func (h *Handler) WithMembers(repo MembersRepository, friends Befriender) *Handler {
	h.members = repo
	h.befriend = friends
	return h
}

func (h *Handler) membersReady() error {
	if h.members == nil || h.sharing == nil || h.graph == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("planning together is not enabled"))
	}
	return nil
}

func (h *Handler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func roleToProto(r Role) tripv1.TripRole {
	switch r {
	case RoleOwner:
		return tripv1.TripRole_TRIP_ROLE_OWNER
	case RoleCoPlanner:
		return tripv1.TripRole_TRIP_ROLE_CO_PLANNER
	case RoleViewer:
		return tripv1.TripRole_TRIP_ROLE_VIEWER
	default:
		return tripv1.TripRole_TRIP_ROLE_UNSPECIFIED
	}
}

func roleFromProto(r tripv1.TripRole) Role {
	switch r {
	case tripv1.TripRole_TRIP_ROLE_CO_PLANNER:
		return RoleCoPlanner
	case tripv1.TripRole_TRIP_ROLE_VIEWER:
		return RoleViewer
	default:
		return RoleNone
	}
}

// onTrip is the one access check for planning together, as a Connect error:
// NotFound for anyone who may not see the trip as a member.
func (h *Handler) onTrip(ctx context.Context, viewer, tripID uuid.UUID) (*Trip, Role, error) {
	t, role, err := NewMemberAccess(h.sharing, h.members, h.graph).Resolve(ctx, viewer, tripID)
	if err != nil {
		return nil, RoleNone, toConnectErr(err)
	}
	return t, role, nil
}

// memberCards is the owner then the members, with their public cards. A
// member whose account is gone has no card and is left out.
func (h *Handler) memberCards(ctx context.Context, t *Trip) ([]*tripv1.TripMember, error) {
	members, err := h.members.Members(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(members)+1)
	ids = append(ids, t.UserID)
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	cards, err := h.graph.PublicUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := []*tripv1.TripMember{{User: cards[t.UserID], Role: tripv1.TripRole_TRIP_ROLE_OWNER}}
	for _, m := range members {
		card := cards[m.UserID]
		if card == nil {
			continue
		}
		out = append(out, &tripv1.TripMember{User: card, Role: roleToProto(m.Role), JoinedAt: timestamppb.New(m.JoinedAt)})
	}
	return out, nil
}

// respondMember is a trip as a member sees it: everything the owner planned,
// since they are planning it too, but not the owner's share code or the chat
// session the trip came from.
func (h *Handler) respondMember(ctx context.Context, t *Trip, role Role) *tripv1.TripDraft {
	p := h.respond(ctx, t)
	p.ShareCode = ""
	p.SourceSessionId = nil
	p.Owner = h.owner(ctx, t.UserID)
	p.ViewerRole = roleToProto(role)
	return p
}

// withMembers adds the member list to a trip the caller is on. Best effort:
// a trip without its avatars is still the trip.
func (h *Handler) withMembers(ctx context.Context, p *tripv1.TripDraft, t *Trip) {
	if h.members == nil || h.graph == nil {
		return
	}
	cards, err := h.memberCards(ctx, t)
	if err != nil {
		h.logWarn("load trip members", err)
		return
	}
	if len(cards) > 1 {
		p.Members = cards
	}
}

// getMemberTrip is GetTrip for a trip the caller does not own.
func (h *Handler) getMemberTrip(ctx context.Context, viewer, id uuid.UUID) (*tripv1.TripDraft, error) {
	t, role, err := h.onTrip(ctx, viewer, id)
	if err != nil {
		return nil, err
	}
	p := h.respondMember(ctx, t, role)
	h.withMembers(ctx, p, t)
	return p, nil
}

func inviteToProto(inv *Invite) *tripv1.TripInvite {
	return &tripv1.TripInvite{
		Code:      inv.Code,
		Url:       InviteURL(inv.Code),
		Role:      roleToProto(inv.Role),
		ExpiresAt: timestamppb.New(inv.ExpiresAt),
		MaxUses:   int32(inv.MaxUses),
		Uses:      int32(inv.Uses),
	}
}

func parseTripID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	return id, nil
}

func (h *Handler) CreateTripInvite(ctx context.Context, req *connect.Request[tripv1.CreateTripInviteRequest]) (*connect.Response[tripv1.TripInvite], error) {
	if err := h.membersReady(); err != nil {
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
	role := roleFromProto(req.Msg.GetRole())
	if role == RoleNone {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("an invite makes co-planners or viewers"))
	}
	// Owner only: the owner-scoped load is the check.
	if _, err := h.repo.GetTrip(ctx, id, uid); err != nil {
		return nil, toConnectErr(err)
	}
	now := h.clock()
	inv, err := h.members.LiveInvite(ctx, id, role, now)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	reused := inv != nil
	if inv == nil {
		inv = &Invite{Code: newShareCode(), TripID: id, Role: role, CreatedBy: uid, ExpiresAt: now.Add(inviteTTL), MaxUses: inviteMaxUses}
		if err := h.members.CreateInvite(ctx, inv); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	h.analytics.Capture(uid.String(), "trip_invite_created", map[string]any{
		"trip_id": id.String(),
		"role":    roleColumn(role),
		"reused":  reused,
	})
	return connect.NewResponse(inviteToProto(inv)), nil
}

// GetTripInvite answers signed-out callers too: the link is how a stranger
// first meets the trip.
func (h *Handler) GetTripInvite(ctx context.Context, req *connect.Request[tripv1.GetTripInviteRequest]) (*connect.Response[tripv1.TripInvitePreview], error) {
	if err := h.membersReady(); err != nil {
		return nil, err
	}
	viewer := viewerID(ctx)
	inv, err := h.members.InviteByCode(ctx, req.Msg.GetCode())
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.sharing.TripByID(ctx, inv.TripID)
	if err != nil {
		return nil, toConnectErr(err)
	}
	already := viewer != uuid.Nil && viewer == t.UserID
	if viewer != uuid.Nil && !already {
		_, blocked, err := h.graph.Relation(ctx, viewer, t.UserID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if blocked {
			return nil, connect.NewError(connect.CodeNotFound, ErrNotFound)
		}
		role, err := h.members.Role(ctx, t.ID, viewer)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		already = role != RoleNone
	}
	members, err := h.memberCards(ctx, t)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	p := &tripv1.TripInvitePreview{
		TripId:        t.ID.String(),
		Title:         t.Title,
		CityName:      t.CityName,
		DayCount:      int32(len(t.Days)),
		Owner:         members[0].GetUser(),
		Members:       members,
		Role:          roleToProto(inv.Role),
		Joinable:      inv.Live(h.clock()),
		AlreadyMember: already,
	}
	p.StartDate = formatOptionalDate(t.StartDate)
	p.EndDate = formatOptionalDate(t.EndDate)
	return connect.NewResponse(p), nil
}

func (h *Handler) AcceptTripInvite(ctx context.Context, req *connect.Request[tripv1.AcceptTripInviteRequest]) (*connect.Response[tripv1.AcceptTripInviteResponse], error) {
	if err := h.membersReady(); err != nil {
		return nil, err
	}
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	code := req.Msg.GetCode()
	inv, err := h.members.InviteByCode(ctx, code)
	if err != nil {
		return nil, toConnectErr(err)
	}
	t, err := h.sharing.TripByID(ctx, inv.TripID)
	if err != nil {
		return nil, toConnectErr(err)
	}
	resp := &tripv1.AcceptTripInviteResponse{TripId: t.ID.String()}
	if uid == t.UserID {
		resp.Role = tripv1.TripRole_TRIP_ROLE_OWNER
		return connect.NewResponse(resp), nil
	}
	_, blocked, err := h.graph.Relation(ctx, uid, t.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if blocked {
		return nil, connect.NewError(connect.CodeNotFound, ErrNotFound)
	}
	now := h.clock()
	n, err := h.members.JoinsSince(ctx, uid, now.Add(-time.Hour))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if n >= maxJoinsPerHour {
		return nil, connect.NewError(connect.CodeResourceExhausted, ErrTooManyJoins)
	}
	joined, err := h.members.Accept(ctx, code, uid, now)
	switch {
	case errors.Is(err, ErrInviteDead):
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	case err != nil:
		return nil, toConnectErr(err)
	}
	resp.Role = roleToProto(joined.Role)
	if joined.New {
		if h.befriend != nil {
			if err := h.befriend.Befriend(ctx, uid, t.UserID); err != nil {
				// Joining is what the caller asked for; the friendship is a
				// courtesy and can be made by hand.
				h.logWarn("befriend trip owner", err)
			}
		}
		h.analytics.Capture(uid.String(), "trip_member_joined", map[string]any{
			"trip_id":  t.ID.String(),
			"role":     roleColumn(joined.Role),
			"new_user": joined.NewUser,
		})
	}
	return connect.NewResponse(resp), nil
}

func (h *Handler) RevokeTripInvite(ctx context.Context, req *connect.Request[tripv1.RevokeTripInviteRequest]) (*connect.Response[tripv1.RevokeTripInviteResponse], error) {
	if err := h.membersReady(); err != nil {
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
	if _, err := h.repo.GetTrip(ctx, id, uid); err != nil {
		return nil, toConnectErr(err)
	}
	if err := h.members.RevokeInvite(ctx, id, req.Msg.GetCode(), h.clock()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&tripv1.RevokeTripInviteResponse{}), nil
}

func (h *Handler) ListTripMembers(ctx context.Context, req *connect.Request[tripv1.ListTripMembersRequest]) (*connect.Response[tripv1.ListTripMembersResponse], error) {
	if err := h.membersReady(); err != nil {
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
	t, _, err := h.onTrip(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	members, err := h.memberCards(ctx, t)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&tripv1.ListTripMembersResponse{Members: members}), nil
}

func (h *Handler) RemoveTripMember(ctx context.Context, req *connect.Request[tripv1.RemoveTripMemberRequest]) (*connect.Response[tripv1.RemoveTripMemberResponse], error) {
	if err := h.membersReady(); err != nil {
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
	target, err := uuid.Parse(req.Msg.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	t, role, err := h.onTrip(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	if target == t.UserID {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the owner cannot be removed from their trip"))
	}
	if role != RoleOwner && target != uid {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only the owner removes other people"))
	}
	if err := h.members.RemoveMember(ctx, id, target); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&tripv1.RemoveTripMemberResponse{}), nil
}

// ownerCards loads the public card of each owner in owners.
func (h *Handler) ownerCards(ctx context.Context, owners []uuid.UUID) map[uuid.UUID]*socialv1.PublicUser {
	if len(owners) == 0 || h.graph == nil {
		return nil
	}
	cards, err := h.graph.PublicUsers(ctx, owners)
	if err != nil {
		h.logWarn("load trip owners", err)
		return nil
	}
	return cards
}
