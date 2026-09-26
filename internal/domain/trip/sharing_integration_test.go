//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	commonv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/social"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func as(id uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, id.String())
}

// The whole sharing story on the real schema: two people become friends
// through an invite, then each visibility level shows exactly to whom it
// should, a copy is independent, and a block hides everything.
func TestTripSharingEndToEnd(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(testTripDB, log)
	socialSvc := social.NewService(social.NewRepository(testTripDB), nil, log)
	h := NewHandler(repo, "https://api.example", nil, nil).
		WithSharing(NewSharingRepository(testTripDB, log), socialSvc)

	ana := newTripUser(t, "share-ana-"+uuid.NewString()+"@loci.test")
	rui := newTripUser(t, "share-rui-"+uuid.NewString()+"@loci.test")
	eve := newTripUser(t, "share-eve-"+uuid.NewString()+"@loci.test")
	ctx := context.Background()

	url := "https://book.example/cafe"
	trip, err := repo.SaveTrip(ctx, &Trip{
		UserID: ana, CityName: "Porto", Title: "Porto weekend",
		Days: []TripDay{{DayNumber: 1, Stops: []TripStop{{Name: "Café Santiago", Notes: "ask for Rui", BookingURL: &url}}}},
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, VisibilityPrivate, trip.Visibility, "a new trip is private")

	// Friends through Ana's invite.
	inv, err := socialSvc.MyInvite(ctx, ana)
	require.NoError(t, err)
	_, _, err = socialSvc.AcceptInvite(ctx, rui, inv.Code)
	require.NoError(t, err)

	getFriend := func(viewer uuid.UUID) error {
		_, err := h.GetFriendTrip(as(viewer), connect.NewRequest(&tripv1.GetFriendTripRequest{TripId: trip.ID.String()}))
		return err
	}
	page := &commonv1.PaginationRequest{Page: 1, PageSize: 20}
	feed := func(viewer uuid.UUID) []*tripv1.TripDraft {
		resp, err := h.ListFriendTrips(as(viewer), connect.NewRequest(&tripv1.ListFriendTripsRequest{Pagination: page}))
		require.NoError(t, err)
		return resp.Msg.GetTrips()
	}

	// PRIVATE: a friend sees nothing.
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(getFriend(rui)))
	assert.Empty(t, feed(rui))

	// FRIENDS: the friend sees it, in the feed too, without private details; a stranger does not.
	set, err := h.SetTripVisibility(as(ana), connect.NewRequest(&tripv1.SetTripVisibilityRequest{
		TripId: trip.ID.String(), Visibility: tripv1.TripVisibility_TRIP_VISIBILITY_FRIENDS,
	}))
	require.NoError(t, err)
	code := set.Msg.GetShareCode()
	require.NotEmpty(t, code)
	assert.Equal(t, ShareURL(code), set.Msg.GetShareUrl())
	require.NoError(t, getFriend(rui))
	items := feed(rui)
	require.Len(t, items, 1)
	assert.Equal(t, "Porto weekend", items[0].GetTitle())
	assert.Equal(t, ana.String(), items[0].GetOwner().GetId())
	assert.Empty(t, items[0].GetShareCode(), "the share code leaked to a friend")
	assert.Empty(t, items[0].GetDays()[0].GetStops()[0].GetNotes(), "private notes leaked")
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(getFriend(eve)))
	_, err = h.GetSharedTrip(context.Background(), connect.NewRequest(&tripv1.GetSharedTripRequest{ShareCode: code}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "a FRIENDS trip opened signed out")

	// LINK: anyone with the code, signed out included; not listed.
	_, err = h.SetTripVisibility(as(ana), connect.NewRequest(&tripv1.SetTripVisibilityRequest{
		TripId: trip.ID.String(), Visibility: tripv1.TripVisibility_TRIP_VISIBILITY_LINK, ShareDetails: true,
	}))
	require.NoError(t, err)
	shared, err := h.GetSharedTrip(context.Background(), connect.NewRequest(&tripv1.GetSharedTripRequest{ShareCode: code}))
	require.NoError(t, err, "the share code changed or the link does not open")
	assert.Equal(t, "ask for Rui", shared.Msg.GetDays()[0].GetStops()[0].GetNotes(), "details shared on purpose are missing")
	assert.Empty(t, feed(rui), "a LINK trip was listed")

	// Copy by link: a new private trip for Eve, independent of the source.
	cp, err := h.CopyTrip(as(eve), connect.NewRequest(&tripv1.CopyTripRequest{
		Source: &tripv1.CopyTripRequest_ShareCode{ShareCode: code},
	}))
	require.NoError(t, err)
	copied, err := repo.GetTrip(ctx, uuid.MustParse(cp.Msg.GetTripId()), eve)
	require.NoError(t, err)
	assert.Equal(t, VisibilityPrivate, copied.Visibility)
	require.NotNil(t, copied.CopiedFromTripID)
	assert.Equal(t, trip.ID, *copied.CopiedFromTripID)
	require.Len(t, copied.Days, 1)
	assert.Equal(t, "Café Santiago", copied.Days[0].Stops[0].Name)
	assert.NotEqual(t, trip.Days[0].Stops[0].ID, copied.Days[0].Stops[0].ID)

	// Profile listing: a stranger sees only PUBLIC, the owner sees all.
	list := func(viewer uuid.UUID) int {
		resp, err := h.ListUserTrips(as(viewer), connect.NewRequest(&tripv1.ListUserTripsRequest{UserId: ana.String(), Pagination: page}))
		require.NoError(t, err)
		return len(resp.Msg.GetTrips())
	}
	assert.Equal(t, 0, list(eve))
	assert.Equal(t, 1, list(ana))

	// A block hides a PUBLIC trip from the blocked side, even by link.
	_, err = h.SetTripVisibility(as(ana), connect.NewRequest(&tripv1.SetTripVisibilityRequest{
		TripId: trip.ID.String(), Visibility: tripv1.TripVisibility_TRIP_VISIBILITY_PUBLIC,
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, list(eve))
	require.NoError(t, socialSvc.Block(ctx, ana, eve))
	_, err = h.GetSharedTrip(as(eve), connect.NewRequest(&tripv1.GetSharedTripRequest{ShareCode: code}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = h.ListUserTrips(as(eve), connect.NewRequest(&tripv1.ListUserTripsRequest{UserId: ana.String(), Pagination: page}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// Only the owner can change visibility.
	_, err = h.SetTripVisibility(as(rui), connect.NewRequest(&tripv1.SetTripVisibilityRequest{
		TripId: trip.ID.String(), Visibility: tripv1.TripVisibility_TRIP_VISIBILITY_PRIVATE,
	}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// Saving the trip does not reset its sharing.
	saved, err := repo.SaveTrip(ctx, trip, trip.Version)
	require.NoError(t, err)
	assert.Equal(t, VisibilityPublic, saved.Visibility)
	require.NotNil(t, saved.ShareCode)
	assert.Equal(t, code, *saved.ShareCode)
}
