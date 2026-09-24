package favorites

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	favoritesv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/favorites/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/favorites/v1/favoritesv1connect"
)

type fakeRepo struct {
	saved map[string]*locitypes.FavoriteItem // key: itemID|contentType
}

func (f *fakeRepo) AddFavorite(_ context.Context, fav *locitypes.FavoriteItem) (*locitypes.FavoriteItem, error) {
	return fav, nil
}
func (f *fakeRepo) RemoveFavorite(context.Context, uuid.UUID, string, string) error { return nil }
func (f *fakeRepo) GetFavorites(context.Context, uuid.UUID, string, int, int) ([]locitypes.FavoriteItem, int, error) {
	return nil, 0, nil
}

func (f *fakeRepo) IsFavorited(context.Context, uuid.UUID, string, string) (bool, error) {
	return false, nil
}

func (f *fakeRepo) GetFavoritesCount(context.Context, uuid.UUID, string) (int, error) { return 0, nil }

func (f *fakeRepo) GetFavoriteByItem(_ context.Context, _ uuid.UUID, itemID, contentType string) (*locitypes.FavoriteItem, error) {
	return f.saved[itemID+"|"+contentType], nil
}

type fakePlaces struct {
	hotels      map[uuid.UUID]*locitypes.HotelDetailedInfo
	restaurants map[uuid.UUID]*locitypes.RestaurantDetailedInfo
	nearHotels  []locitypes.HotelDetailedInfo
	nearRests   []locitypes.RestaurantDetailedInfo
	lastRadius  float64
	lastLimit   int
}

func (f *fakePlaces) GetHotelByID(_ context.Context, id uuid.UUID) (*locitypes.HotelDetailedInfo, error) {
	return f.hotels[id], nil
}

func (f *fakePlaces) GetRestaurantByID(_ context.Context, id uuid.UUID) (*locitypes.RestaurantDetailedInfo, error) {
	return f.restaurants[id], nil
}

func (f *fakePlaces) FindHotelsNear(_ context.Context, _, _, radius float64, limit int) ([]locitypes.HotelDetailedInfo, error) {
	f.lastRadius, f.lastLimit = radius, limit
	return f.nearHotels, nil
}

func (f *fakePlaces) FindRestaurantsNear(_ context.Context, _, _, radius float64, limit int) ([]locitypes.RestaurantDetailedInfo, error) {
	f.lastRadius, f.lastLimit = radius, limit
	return f.nearRests, nil
}

func newTestHandler(repo *fakeRepo, places *fakePlaces) *Handler {
	return NewHandler(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil).WithPlaces(places)
}

func signedIn() context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, uuid.NewString())
}

func snapshot(itemID, contentType, name string) *locitypes.FavoriteItem {
	return &locitypes.FavoriteItem{
		ItemID: itemID, ItemName: name, ContentType: contentType, CityName: "Lisbon",
		Latitude: 38.71, Longitude: -9.14, Rating: 4.5, Category: "Boutique",
		Description: "Saved note", AddedAt: time.Now(),
	}
}

func TestGetHotelDetails_StoredByID(t *testing.T) {
	id := uuid.New()
	website := "https://example.com"
	places := &fakePlaces{hotels: map[uuid.UUID]*locitypes.HotelDetailedInfo{
		id: {ID: id, Name: "Casa", Address: "Rua 1", Website: &website, Tags: []string{"wifi"}},
	}}
	h := newTestHandler(&fakeRepo{}, places)

	res, err := h.GetHotelDetails(signedIn(), connect.NewRequest(&favoritesv1.GetHotelDetailsRequest{HotelId: id.String()}))
	require.NoError(t, err)
	assert.Equal(t, "Casa", res.Msg.Hotel.Name)
	assert.Equal(t, "Rua 1", res.Msg.Hotel.Address)
	assert.Equal(t, website, res.Msg.Hotel.Contact.GetWebsite())
	assert.Equal(t, []string{"wifi"}, res.Msg.Hotel.Amenities)
}

func TestGetHotelDetails_UUIDMissFallsBackToSnapshotAndEnriches(t *testing.T) {
	itemID := uuid.NewString()
	repo := &fakeRepo{saved: map[string]*locitypes.FavoriteItem{itemID + "|hotel": snapshot(itemID, "hotel", "Casa do Largo")}}
	places := &fakePlaces{nearHotels: []locitypes.HotelDetailedInfo{
		{ID: uuid.New(), Name: "Other Inn"},
		{ID: uuid.New(), Name: "Casa do Largo", Address: "Largo 3"},
	}}
	h := newTestHandler(repo, places)

	res, err := h.GetHotelDetails(signedIn(), connect.NewRequest(&favoritesv1.GetHotelDetailsRequest{HotelId: itemID}))
	require.NoError(t, err)
	assert.Equal(t, itemID, res.Msg.Hotel.Id, "keeps the id the client holds")
	assert.Equal(t, "Largo 3", res.Msg.Hotel.Address, "enriched from the stored row at the same spot")
	assert.Equal(t, "Lisbon", res.Msg.Hotel.City, "city from the snapshot")
	assert.Equal(t, float64(snapshotMatchRadiusMeters), places.lastRadius)
}

func TestGetHotelDetails_NonUUIDFallsBackToSnapshot(t *testing.T) {
	itemID := "Casa do Largo|38.71|-9.14"
	repo := &fakeRepo{saved: map[string]*locitypes.FavoriteItem{itemID + "|hotel": snapshot(itemID, "hotel", "Casa do Largo")}}
	h := newTestHandler(repo, &fakePlaces{})

	res, err := h.GetHotelDetails(signedIn(), connect.NewRequest(&favoritesv1.GetHotelDetailsRequest{HotelId: itemID}))
	require.NoError(t, err)
	assert.Equal(t, "Casa do Largo", res.Msg.Hotel.Name)
	assert.Equal(t, 4.5, res.Msg.Hotel.Rating)
	assert.InDelta(t, 38.71, res.Msg.Hotel.Latitude, 1e-9)
}

func TestGetHotelDetails_NotFound(t *testing.T) {
	h := newTestHandler(&fakeRepo{}, &fakePlaces{})
	_, err := h.GetHotelDetails(signedIn(), connect.NewRequest(&favoritesv1.GetHotelDetailsRequest{HotelId: uuid.NewString()}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "not found")
}

func TestGetRestaurantDetails_FallsBackToSnapshot(t *testing.T) {
	itemID := uuid.NewString()
	repo := &fakeRepo{saved: map[string]*locitypes.FavoriteItem{itemID + "|restaurant": snapshot(itemID, "restaurant", "Taberna")}}
	h := newTestHandler(repo, &fakePlaces{})

	res, err := h.GetRestaurantDetails(signedIn(), connect.NewRequest(&favoritesv1.GetRestaurantDetailsRequest{RestaurantId: itemID}))
	require.NoError(t, err)
	assert.Equal(t, "Taberna", res.Msg.Restaurant.Name)
	assert.Equal(t, "Lisbon", res.Msg.Restaurant.City)
}

func TestGetRestaurantDetails_StoredHoursParsed(t *testing.T) {
	id := uuid.New()
	hours := `{"monday":"12:00-23:00"}`
	cuisine := "Portuguese"
	places := &fakePlaces{restaurants: map[uuid.UUID]*locitypes.RestaurantDetailedInfo{
		id: {ID: id, Name: "Taberna", OpeningHours: &hours, CuisineType: &cuisine},
	}}
	h := newTestHandler(&fakeRepo{}, places)

	res, err := h.GetRestaurantDetails(signedIn(), connect.NewRequest(&favoritesv1.GetRestaurantDetailsRequest{RestaurantId: id.String()}))
	require.NoError(t, err)
	assert.Equal(t, "12:00-23:00", res.Msg.Restaurant.Hours["monday"])
	assert.Equal(t, "Portuguese", res.Msg.Restaurant.CuisineType)
}

func TestGetNearby_DefaultsAndClamps(t *testing.T) {
	places := &fakePlaces{nearHotels: []locitypes.HotelDetailedInfo{{ID: uuid.New(), Name: "A"}}}
	h := newTestHandler(&fakeRepo{}, places)

	res, err := h.GetNearbyHotels(signedIn(), connect.NewRequest(&favoritesv1.GetNearbyHotelsRequest{Latitude: 38.7, Longitude: -9.1}))
	require.NoError(t, err)
	assert.Len(t, res.Msg.Hotels, 1)
	assert.Equal(t, float64(defaultNearbyRadiusKm*1000), places.lastRadius)
	assert.Equal(t, defaultNearbyLimit, places.lastLimit)

	_, err = h.GetNearbyRestaurants(signedIn(), connect.NewRequest(&favoritesv1.GetNearbyRestaurantsRequest{
		Latitude: 38.7, Longitude: -9.1, RadiusKm: 500, Limit: 1000,
	}))
	require.NoError(t, err)
	assert.Equal(t, float64(maxNearbyRadiusKm*1000), places.lastRadius)
	assert.Equal(t, maxNearbyLimit, places.lastLimit)

	_, err = h.GetNearbyHotels(signedIn(), connect.NewRequest(&favoritesv1.GetNearbyHotelsRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// Every RPC the service declares must be answered by Handler, not by the
// embedded Unimplemented stub — that stub is how GetHotelDetails shipped as a
// 501 while the web and iOS clients already called it.
func TestEveryRPCImplemented(t *testing.T) {
	h := newTestHandler(&fakeRepo{}, &fakePlaces{})
	hv := reflect.ValueOf(h)
	svc := reflect.TypeOf((*favoritesv1connect.FavoritesServiceHandler)(nil)).Elem()

	for i := range svc.NumMethod() {
		name := svc.Method(i).Name
		t.Run(name, func(t *testing.T) {
			m := hv.MethodByName(name)
			reqPtr := reflect.New(m.Type().In(1).Elem()) // *connect.Request[T]
			msgField := reqPtr.Elem().FieldByName("Msg")
			msgField.Set(reflect.New(msgField.Type().Elem()))

			out := m.Call([]reflect.Value{reflect.ValueOf(signedIn()), reqPtr})
			if errV := out[1]; !errV.IsNil() {
				err, _ := errV.Interface().(error)
				var ce *connect.Error
				if errors.As(err, &ce) {
					assert.NotEqual(t, connect.CodeUnimplemented, ce.Code(), "%s is not implemented", name)
				}
			}
		})
	}
}
