package favorites

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	favoritesv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/favorites/v1"
)

// PlaceReader is the subset of the POI repository the detail and nearby RPCs
// read. Everything here comes from what is already stored; none of it calls
// the model.
type PlaceReader interface {
	GetHotelByID(ctx context.Context, hotelID uuid.UUID) (*locitypes.HotelDetailedInfo, error)
	GetRestaurantByID(ctx context.Context, restaurantID uuid.UUID) (*locitypes.RestaurantDetailedInfo, error)
	FindHotelsNear(ctx context.Context, lat, lon, radiusMeters float64, limit int) ([]locitypes.HotelDetailedInfo, error)
	FindRestaurantsNear(ctx context.Context, lat, lon, radiusMeters float64, limit int) ([]locitypes.RestaurantDetailedInfo, error)
}

const (
	defaultNearbyRadiusKm = 5
	maxNearbyRadiusKm     = 50
	defaultNearbyLimit    = 20
	maxNearbyLimit        = 50

	// A saved snapshot and its stored row are the same place when they sit
	// this close together under the same name.
	snapshotMatchRadiusMeters = 150
)

// errDetailNotFound reads "not found" on purpose: the web detail page keys
// its friendly empty state off that text.
var errDetailNotFound = errors.New("not found")

// GetHotelDetails resolves a saved hotel. The id a client holds is often not
// a hotel_details id — hotels are stored under a fresh id when generated, and
// older saves used a name — so it falls back to the caller's saved snapshot,
// enriched with the stored hotel at the same spot when there is one.
func (h *Handler) GetHotelDetails(
	ctx context.Context,
	req *connect.Request[favoritesv1.GetHotelDetailsRequest],
) (*connect.Response[favoritesv1.GetHotelDetailsResponse], error) {
	id := strings.TrimSpace(req.Msg.HotelId)
	fav := h.savedSnapshot(ctx, id, "hotel")

	hotel, err := h.findHotel(ctx, id, fav)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get hotel", slog.String("hotel_id", id), slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to get hotel"))
	}

	var out *favoritesv1.HotelDetails
	switch {
	case hotel != nil:
		out = hotelToProto(hotel)
		overlaySnapshotOnHotel(out, fav)
	case fav != nil:
		out = hotelFromSnapshot(fav)
	default:
		return nil, connect.NewError(connect.CodeNotFound, errDetailNotFound)
	}
	return connect.NewResponse(&favoritesv1.GetHotelDetailsResponse{Success: true, Hotel: out}), nil
}

// GetRestaurantDetails resolves a saved restaurant the same way
// GetHotelDetails resolves a hotel.
func (h *Handler) GetRestaurantDetails(
	ctx context.Context,
	req *connect.Request[favoritesv1.GetRestaurantDetailsRequest],
) (*connect.Response[favoritesv1.GetRestaurantDetailsResponse], error) {
	id := strings.TrimSpace(req.Msg.RestaurantId)
	fav := h.savedSnapshot(ctx, id, "restaurant")

	restaurant, err := h.findRestaurant(ctx, id, fav)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get restaurant", slog.String("restaurant_id", id), slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to get restaurant"))
	}

	var out *favoritesv1.RestaurantDetails
	switch {
	case restaurant != nil:
		out = restaurantToProto(restaurant)
		overlaySnapshotOnRestaurant(out, fav)
	case fav != nil:
		out = restaurantFromSnapshot(fav)
	default:
		return nil, connect.NewError(connect.CodeNotFound, errDetailNotFound)
	}
	return connect.NewResponse(&favoritesv1.GetRestaurantDetailsResponse{Success: true, Restaurant: out}), nil
}

// GetNearbyHotels lists stored hotels around a point, nearest first.
func (h *Handler) GetNearbyHotels(
	ctx context.Context,
	req *connect.Request[favoritesv1.GetNearbyHotelsRequest],
) (*connect.Response[favoritesv1.GetNearbyHotelsResponse], error) {
	radiusM, limit, err := nearbyBounds(req.Msg.Latitude, req.Msg.Longitude, req.Msg.RadiusKm, req.Msg.Limit)
	if err != nil {
		return nil, err
	}
	if h.places == nil {
		return connect.NewResponse(&favoritesv1.GetNearbyHotelsResponse{}), nil
	}
	hotels, err := h.places.FindHotelsNear(ctx, req.Msg.Latitude, req.Msg.Longitude, radiusM, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to find nearby hotels", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to find nearby hotels"))
	}
	out := make([]*favoritesv1.HotelDetails, len(hotels))
	for i := range hotels {
		out[i] = hotelToProto(&hotels[i])
	}
	return connect.NewResponse(&favoritesv1.GetNearbyHotelsResponse{Hotels: out, TotalCount: int32(len(out))}), nil
}

// GetNearbyRestaurants lists stored restaurants around a point, nearest first.
func (h *Handler) GetNearbyRestaurants(
	ctx context.Context,
	req *connect.Request[favoritesv1.GetNearbyRestaurantsRequest],
) (*connect.Response[favoritesv1.GetNearbyRestaurantsResponse], error) {
	radiusM, limit, err := nearbyBounds(req.Msg.Latitude, req.Msg.Longitude, req.Msg.RadiusKm, req.Msg.Limit)
	if err != nil {
		return nil, err
	}
	if h.places == nil {
		return connect.NewResponse(&favoritesv1.GetNearbyRestaurantsResponse{}), nil
	}
	restaurants, err := h.places.FindRestaurantsNear(ctx, req.Msg.Latitude, req.Msg.Longitude, radiusM, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to find nearby restaurants", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to find nearby restaurants"))
	}
	out := make([]*favoritesv1.RestaurantDetails, len(restaurants))
	for i := range restaurants {
		out[i] = restaurantToProto(&restaurants[i])
	}
	return connect.NewResponse(&favoritesv1.GetNearbyRestaurantsResponse{Restaurants: out, TotalCount: int32(len(out))}), nil
}

// savedSnapshot returns the caller's saved row for an item, or nil. A lookup
// failure only costs the fallback, so it is logged rather than returned.
func (h *Handler) savedSnapshot(ctx context.Context, itemID, contentType string) *locitypes.FavoriteItem {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return nil
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil
	}
	fav, err := h.repo.GetFavoriteByItem(ctx, userID, itemID, contentType)
	if err != nil {
		h.logger.WarnContext(ctx, "saved snapshot lookup failed", slog.String("item_id", itemID), slog.Any("error", err))
		return nil
	}
	return fav
}

func (h *Handler) findHotel(ctx context.Context, id string, fav *locitypes.FavoriteItem) (*locitypes.HotelDetailedInfo, error) {
	if h.places == nil {
		return nil, nil
	}
	if hotelID, err := uuid.Parse(id); err == nil {
		hotel, err := h.places.GetHotelByID(ctx, hotelID)
		if err != nil || hotel != nil {
			return hotel, err
		}
	}
	if !hasLocation(fav) {
		return nil, nil
	}
	nearby, err := h.places.FindHotelsNear(ctx, fav.Latitude, fav.Longitude, snapshotMatchRadiusMeters, 10)
	if err != nil {
		return nil, err
	}
	for i := range nearby {
		if sameName(nearby[i].Name, fav.ItemName) {
			return &nearby[i], nil
		}
	}
	return nil, nil
}

func (h *Handler) findRestaurant(ctx context.Context, id string, fav *locitypes.FavoriteItem) (*locitypes.RestaurantDetailedInfo, error) {
	if h.places == nil {
		return nil, nil
	}
	if restaurantID, err := uuid.Parse(id); err == nil {
		restaurant, err := h.places.GetRestaurantByID(ctx, restaurantID)
		if err != nil || restaurant != nil {
			return restaurant, err
		}
	}
	if !hasLocation(fav) {
		return nil, nil
	}
	nearby, err := h.places.FindRestaurantsNear(ctx, fav.Latitude, fav.Longitude, snapshotMatchRadiusMeters, 10)
	if err != nil {
		return nil, err
	}
	for i := range nearby {
		if sameName(nearby[i].Name, fav.ItemName) {
			return &nearby[i], nil
		}
	}
	return nil, nil
}

func nearbyBounds(lat, lon, radiusKm float64, limit int32) (float64, int, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 || (lat == 0 && lon == 0) {
		return 0, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("latitude and longitude are required"))
	}
	if radiusKm <= 0 {
		radiusKm = defaultNearbyRadiusKm
	}
	radiusKm = min(radiusKm, maxNearbyRadiusKm)
	n := int(limit)
	if n <= 0 {
		n = defaultNearbyLimit
	}
	n = min(n, maxNearbyLimit)
	return radiusKm * 1000, n, nil
}

func hasLocation(fav *locitypes.FavoriteItem) bool {
	return fav != nil && fav.ItemName != "" && (fav.Latitude != 0 || fav.Longitude != 0)
}

func sameName(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	return a != "" && b != "" && (a == b || strings.Contains(a, b) || strings.Contains(b, a))
}
