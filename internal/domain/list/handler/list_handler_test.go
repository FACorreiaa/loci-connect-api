package handler

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	listpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/list"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	itinerarylist "github.com/FACorreiaa/loci-connect-api/internal/domain/list"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// stubService implements only what these tests call; anything else panics on
// the nil embedded interface, which is the point.
type stubService struct {
	itinerarylist.Service

	lists   []*locitypes.List
	details map[uuid.UUID]*locitypes.ListWithItems
	places  map[uuid.UUID]locitypes.POIDetailedInfo
	update  locitypes.UpdateListRequest

	removedAny  []uuid.UUID
	removedType []locitypes.ContentType
}

func (s *stubService) GetAllUserLists(context.Context, uuid.UUID) ([]*locitypes.List, error) {
	return s.lists, nil
}

func (s *stubService) GetListDetails(_ context.Context, listID, _ uuid.UUID) (*locitypes.ListWithItems, error) {
	if d, ok := s.details[listID]; ok {
		return d, nil
	}
	return nil, locitypes.ErrNotFound
}

func (s *stubService) GetListPlaces(context.Context, []*locitypes.ListItem) (map[uuid.UUID]locitypes.POIDetailedInfo, error) {
	return s.places, nil
}

func (s *stubService) UpdateListDetails(_ context.Context, listID, userID uuid.UUID, p locitypes.UpdateListRequest) (*locitypes.List, error) {
	s.update = p
	return &locitypes.List{ID: listID, UserID: userID}, nil
}

func (s *stubService) RemoveListItem(_ context.Context, _, _, itemID uuid.UUID) error {
	s.removedAny = append(s.removedAny, itemID)
	return nil
}

func (s *stubService) RemoveListItemOfType(_ context.Context, _, _, _ uuid.UUID, ct locitypes.ContentType) error {
	s.removedType = append(s.removedType, ct)
	return nil
}

func signedIn(t *testing.T) (context.Context, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	return context.WithValue(context.Background(), interceptors.UserIDKey, id.String()), id
}

func newTestHandler(svc itinerarylist.Service) *ListHandler {
	return NewListHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGetLists_ReturnsBothKindsAndPages(t *testing.T) {
	ctx, _ := signedIn(t)
	now := time.Now()
	custom := &locitypes.List{ID: uuid.New(), Name: "Favourites", ItemCount: 2, CreatedAt: now, UpdatedAt: now}
	trip := &locitypes.List{ID: uuid.New(), Name: "Porto", IsItinerary: true, CreatedAt: now, UpdatedAt: now}
	third := &locitypes.List{ID: uuid.New(), Name: "Coffee", CreatedAt: now, UpdatedAt: now}
	svc := &stubService{lists: []*locitypes.List{custom, trip, third}}
	h := newTestHandler(svc)

	res, err := h.GetLists(ctx, connect.NewRequest(&listpb.GetListsRequest{}))
	require.NoError(t, err)
	require.Len(t, res.Msg.Lists, 3, "limit 0 returns every list, custom lists included")
	assert.Equal(t, int32(3), res.Msg.TotalCount)
	assert.Equal(t, int32(2), res.Msg.Lists[0].List.ItemCount)
	assert.Empty(t, res.Msg.Lists[0].List.CityId, "no city must be empty, not the nil uuid")

	res, err = h.GetLists(ctx, connect.NewRequest(&listpb.GetListsRequest{Limit: 1, Offset: 1}))
	require.NoError(t, err)
	require.Len(t, res.Msg.Lists, 1)
	assert.Equal(t, "Porto", res.Msg.Lists[0].List.Name)
	assert.Equal(t, int32(3), res.Msg.TotalCount, "total_count is before paging")

	res, err = h.GetLists(ctx, connect.NewRequest(&listpb.GetListsRequest{Offset: 10}))
	require.NoError(t, err)
	assert.Empty(t, res.Msg.Lists)
}

func TestGetLists_IncludeItems(t *testing.T) {
	ctx, _ := signedIn(t)
	list := &locitypes.List{ID: uuid.New(), Name: "Favourites"}
	poi := uuid.New()
	svc := &stubService{
		lists: []*locitypes.List{list},
		details: map[uuid.UUID]*locitypes.ListWithItems{list.ID: {
			List:  *list,
			Items: []*locitypes.ListItem{{ListID: list.ID, ItemID: poi, PoiID: poi, ContentType: locitypes.ContentTypePOI}},
		}},
	}

	res, err := newTestHandler(svc).GetLists(ctx, connect.NewRequest(&listpb.GetListsRequest{IncludeItems: true}))
	require.NoError(t, err)
	require.Len(t, res.Msg.Lists[0].Items, 1)
	assert.Equal(t, poi.String(), res.Msg.Lists[0].Items[0].ItemId)
	assert.Equal(t, listpb.ContentType_CONTENT_TYPE_POI, res.Msg.Lists[0].Items[0].ContentType)
}

func TestGetList_DetailedItemsFillThePlaceByType(t *testing.T) {
	ctx, _ := signedIn(t)
	listID := uuid.New()
	poiID, restID, hotelID, goneID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	items := []*locitypes.ListItem{
		{ListID: listID, ItemID: poiID, PoiID: poiID, ContentType: locitypes.ContentTypePOI},
		{ListID: listID, ItemID: restID, ContentType: locitypes.ContentTypeRestaurant},
		{ListID: listID, ItemID: hotelID, ContentType: locitypes.ContentTypeHotel},
		{ListID: listID, ItemID: goneID, ContentType: locitypes.ContentTypePOI},
	}
	svc := &stubService{
		details: map[uuid.UUID]*locitypes.ListWithItems{listID: {List: locitypes.List{ID: listID}, Items: items}},
		places: map[uuid.UUID]locitypes.POIDetailedInfo{
			poiID:   {ID: poiID, Name: "Torre", Rating: 4.5},
			restID:  {ID: restID, Name: "Tasca", Rating: 9}, // out of the contract's range
			hotelID: {ID: hotelID, Name: "Pestana"},
		},
	}
	h := newTestHandler(svc)

	res, err := h.GetList(ctx, connect.NewRequest(&listpb.GetListRequest{ListId: listID.String(), IncludeDetailedItems: true}))
	require.NoError(t, err)
	got := res.Msg.List.Items
	require.Len(t, got, 4)

	require.NotNil(t, got[0].Poi)
	assert.Equal(t, "Torre", got[0].Poi.Name)
	assert.Equal(t, poiID.String(), got[0].ListItem.PoiId)

	require.NotNil(t, got[1].Restaurant)
	assert.Equal(t, "Tasca", got[1].Restaurant.Poi.Name)
	assert.Equal(t, float64(5), got[1].Restaurant.Poi.Rating, "rating is clamped to 0..5")
	assert.Nil(t, got[1].Poi)
	assert.Empty(t, got[1].ListItem.PoiId, "poi_id is only set for POI items")

	require.NotNil(t, got[2].Hotel)
	assert.Equal(t, "Pestana", got[2].Hotel.Poi.Name)

	assert.Nil(t, got[3].Poi, "a place that is gone leaves the content unset")
	assert.NotNil(t, got[3].ListItem)

	// Without the flag, items still come back, with no place filled in.
	res, err = h.GetList(ctx, connect.NewRequest(&listpb.GetListRequest{ListId: listID.String()}))
	require.NoError(t, err)
	require.Len(t, res.Msg.List.Items, 4)
	assert.Nil(t, res.Msg.List.Items[0].Poi)
}

func TestGetList_MissingListIsNotFound(t *testing.T) {
	ctx, _ := signedIn(t)
	_, err := newTestHandler(&stubService{}).GetList(ctx, connect.NewRequest(&listpb.GetListRequest{ListId: uuid.NewString()}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestUpdateList_IsItineraryOnlyWhenSet(t *testing.T) {
	ctx, _ := signedIn(t)
	svc := &stubService{}
	h := newTestHandler(svc)
	listID := uuid.NewString()

	_, err := h.UpdateList(ctx, connect.NewRequest(&listpb.UpdateListRequest{ListId: listID, Name: "n"}))
	require.NoError(t, err)
	assert.Nil(t, svc.update.IsItinerary)

	yes := true
	_, err = h.UpdateList(ctx, connect.NewRequest(&listpb.UpdateListRequest{ListId: listID, IsItinerary: &yes}))
	require.NoError(t, err)
	require.NotNil(t, svc.update.IsItinerary)
	assert.True(t, *svc.update.IsItinerary)

	_, err = h.UpdateList(ctx, connect.NewRequest(&listpb.UpdateListRequest{ListId: listID, CityId: "not-a-uuid"}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateList_RejectsAMalformedCityID(t *testing.T) {
	ctx, _ := signedIn(t)
	_, err := newTestHandler(&stubService{}).CreateList(ctx, connect.NewRequest(&listpb.CreateListRequest{Name: "n", CityId: "lisbon"}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestRemoveListItem_ContentTypeNarrowsTheMatch(t *testing.T) {
	ctx, _ := signedIn(t)
	svc := &stubService{}
	h := newTestHandler(svc)
	listID, itemID := uuid.NewString(), uuid.New()

	_, err := h.RemoveListItem(ctx, connect.NewRequest(&listpb.RemoveListItemRequest{ListId: listID, ItemId: itemID.String()}))
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{itemID}, svc.removedAny, "UNSPECIFIED removes whatever the type")

	_, err = h.RemoveListItem(ctx, connect.NewRequest(&listpb.RemoveListItemRequest{
		ListId: listID, ItemId: itemID.String(), ContentType: listpb.ContentType_CONTENT_TYPE_HOTEL,
	}))
	require.NoError(t, err)
	assert.Equal(t, []locitypes.ContentType{locitypes.ContentTypeHotel}, svc.removedType)

	_, err = h.RemoveListItem(ctx, connect.NewRequest(&listpb.RemoveListItemRequest{ListId: listID, ItemId: "x"}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestRemoveListItem_RequiresSignIn(t *testing.T) {
	_, err := newTestHandler(&stubService{}).RemoveListItem(context.Background(), connect.NewRequest(&listpb.RemoveListItemRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
