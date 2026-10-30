package trip

import (
	"context"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

// MemberAccess answers who is on a trip and what they may do there. It is the one
// check planning together uses, whether the caller is the trip page or a
// proposal being applied.
type MemberAccess struct {
	sharing SharingRepository
	members MembersRepository
	graph   SocialGraph
}

func NewMemberAccess(sharing SharingRepository, members MembersRepository, graph SocialGraph) *MemberAccess {
	return &MemberAccess{sharing: sharing, members: members, graph: graph}
}

// Resolve loads a trip and viewer's role on it. Anyone not on the trip, and a
// member either side has blocked, gets ErrNotFound: the trip's existence is
// not theirs to know.
func (a *MemberAccess) Resolve(ctx context.Context, viewer, tripID uuid.UUID) (*Trip, Role, error) {
	t, err := a.sharing.TripByID(ctx, tripID)
	if err != nil {
		return nil, RoleNone, err
	}
	if viewer != uuid.Nil && viewer == t.UserID {
		return t, RoleOwner, nil
	}
	if viewer == uuid.Nil {
		return nil, RoleNone, ErrNotFound
	}
	role, err := a.members.Role(ctx, tripID, viewer)
	if err != nil {
		return nil, RoleNone, err
	}
	if role == RoleNone {
		return nil, RoleNone, ErrNotFound
	}
	_, blocked, err := a.graph.Relation(ctx, viewer, t.UserID)
	if err != nil {
		return nil, RoleNone, err
	}
	// Only members get this far, so a private trip stays private to strangers.
	if !CanSeeAsMember(viewer, t.UserID, role, VisibilityPrivate, AccessByID, false, blocked) {
		return nil, RoleNone, ErrNotFound
	}
	return t, role, nil
}

// Everyone is the trip's owner and its members, owner first.
func (a *MemberAccess) Everyone(ctx context.Context, t *Trip) ([]uuid.UUID, error) {
	members, err := a.members.Members(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(members)+1)
	out = append(out, t.UserID)
	for _, m := range members {
		out = append(out, m.UserID)
	}
	return out, nil
}

// CoPlannersMayApply is the owner's rule on whether co-planners apply
// suggestions too.
func (a *MemberAccess) CoPlannersMayApply(ctx context.Context, tripID uuid.UUID) (bool, error) {
	return a.members.CoPlannersMayApply(ctx, tripID)
}

// PublicUsers is the people layer's public cards, for naming who did what.
func (a *MemberAccess) PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	return a.graph.PublicUsers(ctx, ids)
}
