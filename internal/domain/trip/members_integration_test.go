//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	commonv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/social"
)

// Planning together on the real schema: the owner mints a link, a stranger
// previews it signed out, joins, becomes the owner's friend and opens the
// private trip; the link is reused, runs out and can be revoked; members
// leave or are removed; a block takes a member off the trip's reads.
func TestTripMembersEndToEnd(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, log)
	socialRepo := social.NewRepository(testTripDB)
	socialSvc := social.NewService(socialRepo, nil, log)
	members := NewMembersRepository(testTripDB, log)
	h := NewHandler(repo, "https://api.example", nil, nil).
		WithSharing(NewSharingRepository(testTripDB, log), socialSvc).
		WithMembers(members, socialSvc)
	now := time.Now()
	h.now = func() time.Time { return now }

	ana := newTripUser(t, "members-ana-"+uuid.NewString()+"@loci.test")
	rui := newTripUser(t, "members-rui-"+uuid.NewString()+"@loci.test")
	eve := newTripUser(t, "members-eve-"+uuid.NewString()+"@loci.test")
	ctx := context.Background()

	trip, err := repo.SaveTrip(ctx, &Trip{
		UserID: ana, CityName: "Lisbon", Title: "Lisbon long weekend",
		Days: []TripDay{{DayNumber: 1, Stops: []TripStop{{Name: "Miradouro da Graça", Notes: "sunset"}}}},
	}, 0)
	require.NoError(t, err)
	require.Equal(t, VisibilityPrivate, trip.Visibility)

	getTrip := func(viewer uuid.UUID) (*tripv1.TripDraft, error) {
		resp, err := h.GetTrip(as(viewer), connect.NewRequest(&tripv1.GetTripRequest{TripId: trip.ID.String()}))
		if err != nil {
			return nil, err
		}
		return resp.Msg, nil
	}

	// A stranger cannot open the private trip, nor mint its invite.
	_, err = getTrip(rui)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = h.CreateTripInvite(as(rui), connect.NewRequest(&tripv1.CreateTripInviteRequest{
		TripId: trip.ID.String(), Role: tripv1.TripRole_TRIP_ROLE_CO_PLANNER,
	}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// The owner's link is reused on the next tap.
	mint := func() *tripv1.TripInvite {
		resp, err := h.CreateTripInvite(as(ana), connect.NewRequest(&tripv1.CreateTripInviteRequest{
			TripId: trip.ID.String(), Role: tripv1.TripRole_TRIP_ROLE_CO_PLANNER,
		}))
		require.NoError(t, err)
		return resp.Msg
	}
	inv := mint()
	assert.Equal(t, InviteURL(inv.GetCode()), inv.GetUrl())
	assert.Equal(t, inv.GetCode(), mint().GetCode(), "a live invite is reused")

	// Signed out, the preview shows the trip's outline and its owner, not its stops.
	prev, err := h.GetTripInvite(context.Background(), connect.NewRequest(&tripv1.GetTripInviteRequest{Code: inv.GetCode()}))
	require.NoError(t, err)
	assert.Equal(t, "Lisbon", prev.Msg.GetCityName())
	assert.Equal(t, int32(1), prev.Msg.GetDayCount())
	assert.Equal(t, ana.String(), prev.Msg.GetOwner().GetId())
	assert.True(t, prev.Msg.GetJoinable())
	assert.False(t, prev.Msg.GetAlreadyMember())

	// Rui joins as a co-planner, becomes Ana's friend, and opens the private trip in full.
	acc, err := h.AcceptTripInvite(as(rui), connect.NewRequest(&tripv1.AcceptTripInviteRequest{Code: inv.GetCode()}))
	require.NoError(t, err)
	assert.Equal(t, trip.ID.String(), acc.Msg.GetTripId())
	assert.Equal(t, tripv1.TripRole_TRIP_ROLE_CO_PLANNER, acc.Msg.GetRole())
	friends, _, err := socialSvc.Relation(ctx, rui, ana)
	require.NoError(t, err)
	assert.True(t, friends, "joining a trip makes the pair friends")

	got, err := getTrip(rui)
	require.NoError(t, err)
	assert.Equal(t, tripv1.TripRole_TRIP_ROLE_CO_PLANNER, got.GetViewerRole())
	assert.Equal(t, "sunset", got.GetDays()[0].GetStops()[0].GetNotes(), "co-planners see what the owner planned")
	assert.Empty(t, got.GetShareCode())
	assert.Equal(t, ana.String(), got.GetOwner().GetId())
	require.Len(t, got.GetMembers(), 2)
	assert.Equal(t, tripv1.TripRole_TRIP_ROLE_OWNER, got.GetMembers()[0].GetRole())
	assert.Equal(t, rui.String(), got.GetMembers()[1].GetUser().GetId())

	owned, err := getTrip(ana)
	require.NoError(t, err)
	assert.Equal(t, tripv1.TripRole_TRIP_ROLE_OWNER, owned.GetViewerRole())
	assert.Len(t, owned.GetMembers(), 2)

	// Joining again keeps the role and uses nothing up.
	_, err = h.AcceptTripInvite(as(rui), connect.NewRequest(&tripv1.AcceptTripInviteRequest{Code: inv.GetCode()}))
	require.NoError(t, err)
	stored, err := members.InviteByCode(ctx, inv.GetCode())
	require.NoError(t, err)
	assert.Equal(t, 1, stored.Uses)
	prev, err = h.GetTripInvite(as(rui), connect.NewRequest(&tripv1.GetTripInviteRequest{Code: inv.GetCode()}))
	require.NoError(t, err)
	assert.True(t, prev.Msg.GetAlreadyMember())

	// The trip shows in Rui's list only when member trips are asked for.
	page := &commonv1.PaginationRequest{Page: 1, PageSize: 20}
	list := func(include bool) []*tripv1.TripDraft {
		resp, err := h.ListTrips(as(rui), connect.NewRequest(&tripv1.ListTripsRequest{Pagination: page, IncludeMember: include}))
		require.NoError(t, err)
		return resp.Msg.GetTrips()
	}
	assert.Empty(t, list(false))
	mine := list(true)
	require.Len(t, mine, 1)
	assert.Equal(t, tripv1.TripRole_TRIP_ROLE_CO_PLANNER, mine[0].GetViewerRole())
	assert.Equal(t, ana.String(), mine[0].GetOwner().GetId())

	// A member cannot remove someone else, but may leave; the owner cannot be removed.
	_, err = h.RemoveTripMember(as(rui), connect.NewRequest(&tripv1.RemoveTripMemberRequest{TripId: trip.ID.String(), UserId: ana.String()}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// A block takes Rui off the trip's reads without anyone removing him.
	require.NoError(t, socialSvc.Block(ctx, ana, rui))
	_, err = getTrip(rui)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = h.ListTripMembers(as(rui), connect.NewRequest(&tripv1.ListTripMembersRequest{TripId: trip.ID.String()}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = h.GetTripInvite(as(rui), connect.NewRequest(&tripv1.GetTripInviteRequest{Code: inv.GetCode()}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	require.NoError(t, socialRepo.Unblock(ctx, ana, rui))

	// Eve joins; Rui cannot remove her; Ana can; Rui leaves on his own.
	_, err = h.AcceptTripInvite(as(eve), connect.NewRequest(&tripv1.AcceptTripInviteRequest{Code: inv.GetCode()}))
	require.NoError(t, err)
	_, err = h.RemoveTripMember(as(rui), connect.NewRequest(&tripv1.RemoveTripMemberRequest{TripId: trip.ID.String(), UserId: eve.String()}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = h.RemoveTripMember(as(ana), connect.NewRequest(&tripv1.RemoveTripMemberRequest{TripId: trip.ID.String(), UserId: eve.String()}))
	require.NoError(t, err)
	_, err = getTrip(eve)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = h.RemoveTripMember(as(rui), connect.NewRequest(&tripv1.RemoveTripMemberRequest{TripId: trip.ID.String(), UserId: rui.String()}))
	require.NoError(t, err)
	_, err = getTrip(rui)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// A revoked invite is dead for newcomers.
	_, err = h.RevokeTripInvite(as(ana), connect.NewRequest(&tripv1.RevokeTripInviteRequest{TripId: trip.ID.String(), Code: inv.GetCode()}))
	require.NoError(t, err)
	late := newTripUser(t, "members-late-"+uuid.NewString()+"@loci.test")
	_, err = h.AcceptTripInvite(as(late), connect.NewRequest(&tripv1.AcceptTripInviteRequest{Code: inv.GetCode()}))
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.NotEqual(t, inv.GetCode(), mint().GetCode(), "a revoked invite is not reused")

	// An expired one too.
	viewerInv, err := h.CreateTripInvite(as(ana), connect.NewRequest(&tripv1.CreateTripInviteRequest{
		TripId: trip.ID.String(), Role: tripv1.TripRole_TRIP_ROLE_VIEWER,
	}))
	require.NoError(t, err)
	now = now.Add(inviteTTL + time.Minute)
	_, err = h.AcceptTripInvite(as(late), connect.NewRequest(&tripv1.AcceptTripInviteRequest{Code: viewerInv.Msg.GetCode()}))
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	prev, err = h.GetTripInvite(context.Background(), connect.NewRequest(&tripv1.GetTripInviteRequest{Code: viewerInv.Msg.GetCode()}))
	require.NoError(t, err)
	assert.False(t, prev.Msg.GetJoinable())

	// An unknown code is NotFound.
	_, err = h.GetTripInvite(context.Background(), connect.NewRequest(&tripv1.GetTripInviteRequest{Code: "nope"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// A link is good for inviteMaxUses people; the next one is told it is used up.
func TestTripInviteUseLimit(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, log)
	members := NewMembersRepository(testTripDB, log)
	ctx := context.Background()

	owner := newTripUser(t, "limit-owner-"+uuid.NewString()+"@loci.test")
	trip, err := repo.SaveTrip(ctx, &Trip{UserID: owner, CityName: "Porto", Title: "Porto"}, 0)
	require.NoError(t, err)
	inv := &Invite{Code: newShareCode(), TripID: trip.ID, Role: RoleViewer, CreatedBy: owner, ExpiresAt: time.Now().Add(time.Hour), MaxUses: 2}
	require.NoError(t, members.CreateInvite(ctx, inv))

	for i := 0; i < 2; i++ {
		j, err := members.Accept(ctx, inv.Code, newTripUser(t, "limit-"+uuid.NewString()+"@loci.test"), time.Now())
		require.NoError(t, err)
		assert.True(t, j.New)
		assert.Equal(t, RoleViewer, j.Role)
	}
	_, err = members.Accept(ctx, inv.Code, newTripUser(t, "limit-"+uuid.NewString()+"@loci.test"), time.Now())
	assert.ErrorIs(t, err, ErrInviteDead)
}

// Joining is capped per hour, so a leaked account cannot sweep up invites.
func TestAcceptTripInviteRateLimit(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, log)
	socialSvc := social.NewService(social.NewRepository(testTripDB), nil, log)
	members := NewMembersRepository(testTripDB, log)
	h := NewHandler(repo, "https://api.example", nil, nil).
		WithSharing(NewSharingRepository(testTripDB, log), socialSvc).
		WithMembers(members, socialSvc)
	ctx := context.Background()

	joiner := newTripUser(t, "rate-joiner-"+uuid.NewString()+"@loci.test")
	invite := func() string {
		owner := newTripUser(t, "rate-owner-"+uuid.NewString()+"@loci.test")
		trip, err := repo.SaveTrip(ctx, &Trip{UserID: owner, CityName: "Faro", Title: "Faro"}, 0)
		require.NoError(t, err)
		inv := &Invite{Code: newShareCode(), TripID: trip.ID, Role: RoleViewer, CreatedBy: owner, ExpiresAt: time.Now().Add(time.Hour), MaxUses: 1}
		require.NoError(t, members.CreateInvite(ctx, inv))
		return inv.Code
	}
	for i := 0; i < maxJoinsPerHour; i++ {
		_, err := members.Accept(ctx, invite(), joiner, time.Now())
		require.NoError(t, err)
	}
	_, err := h.AcceptTripInvite(as(joiner), connect.NewRequest(&tripv1.AcceptTripInviteRequest{Code: invite()}))
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
}
