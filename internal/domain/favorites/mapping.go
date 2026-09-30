package favorites

import (
	"encoding/json"

	"github.com/FACorreiaa/go-utils/pkg/util"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	favoritesv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/favorites/v1"
)

func hotelToProto(h *locitypes.HotelDetailedInfo) *favoritesv1.HotelDetails {
	out := &favoritesv1.HotelDetails{
		Id:          h.ID.String(),
		Name:        h.Name,
		City:        h.City,
		Description: h.Description,
		Latitude:    h.Latitude,
		Longitude:   h.Longitude,
		Address:     h.Address,
		Category:    h.Category,
		Rating:      h.Rating,
		PriceRange:  util.Val(h.PriceRange),
		Amenities:   h.Tags,
		Images:      h.Images,
		Phone:       util.Val(h.PhoneNumber),
		Website:     util.Val(h.Website),
	}
	if h.LlmInteractionID != uuid.Nil {
		out.LlmInteractionId = h.LlmInteractionID.String()
	}
	if out.Phone != "" || out.Website != "" {
		out.Contact = &favoritesv1.HotelContact{Phone: out.Phone, Website: out.Website}
	}
	return out
}

func restaurantToProto(r *locitypes.RestaurantDetailedInfo) *favoritesv1.RestaurantDetails {
	out := &favoritesv1.RestaurantDetails{
		Id:          r.ID.String(),
		Name:        r.Name,
		City:        r.City,
		Description: r.Description,
		Latitude:    r.Latitude,
		Longitude:   r.Longitude,
		Address:     util.Val(r.Address),
		Category:    r.Category,
		Rating:      r.Rating,
		CuisineType: util.Val(r.CuisineType),
		PriceRange:  util.Val(r.PriceLevel),
		Tags:        r.Tags,
		Images:      r.Images,
		Phone:       util.Val(r.PhoneNumber),
		Website:     util.Val(r.Website),
		Hours:       openingHours(util.Val(r.OpeningHours)),
	}
	if r.LlmInteractionID != uuid.Nil {
		out.LlmInteractionId = r.LlmInteractionID.String()
	}
	if out.Phone != "" || out.Website != "" {
		out.Contact = &favoritesv1.RestaurantContact{Phone: out.Phone, Website: out.Website}
	}
	return out
}

// The saved snapshot knows the city, the id the client holds and what the
// user wrote about it; the stored row usually knows none of those.
func overlaySnapshotOnHotel(out *favoritesv1.HotelDetails, fav *locitypes.FavoriteItem) {
	if fav == nil {
		return
	}
	out.Id = fav.ItemID
	out.City = util.OrDefault(out.City, fav.CityName)
	out.Description = util.OrDefault(out.Description, fav.Description)
	out.Category = util.OrDefault(out.Category, fav.Category)
	if out.Rating == 0 {
		out.Rating = fav.Rating
	}
	out.CreatedAt = timestamppb.New(fav.AddedAt)
}

func overlaySnapshotOnRestaurant(out *favoritesv1.RestaurantDetails, fav *locitypes.FavoriteItem) {
	if fav == nil {
		return
	}
	out.Id = fav.ItemID
	out.City = util.OrDefault(out.City, fav.CityName)
	out.Description = util.OrDefault(out.Description, fav.Description)
	out.Category = util.OrDefault(out.Category, fav.Category)
	if out.Rating == 0 {
		out.Rating = fav.Rating
	}
	out.CreatedAt = timestamppb.New(fav.AddedAt)
}

func hotelFromSnapshot(fav *locitypes.FavoriteItem) *favoritesv1.HotelDetails {
	return &favoritesv1.HotelDetails{
		Id:          fav.ItemID,
		Name:        fav.ItemName,
		City:        fav.CityName,
		Description: fav.Description,
		Latitude:    fav.Latitude,
		Longitude:   fav.Longitude,
		Category:    fav.Category,
		Rating:      fav.Rating,
		CreatedAt:   timestamppb.New(fav.AddedAt),
	}
}

func restaurantFromSnapshot(fav *locitypes.FavoriteItem) *favoritesv1.RestaurantDetails {
	return &favoritesv1.RestaurantDetails{
		Id:          fav.ItemID,
		Name:        fav.ItemName,
		City:        fav.CityName,
		Description: fav.Description,
		Latitude:    fav.Latitude,
		Longitude:   fav.Longitude,
		Category:    fav.Category,
		Rating:      fav.Rating,
		CreatedAt:   timestamppb.New(fav.AddedAt),
	}
}

// openingHours reads stored hours when they are a flat day→hours object and
// drops anything else rather than guessing at its shape.
func openingHours(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var hours map[string]string
	if err := json.Unmarshal([]byte(raw), &hours); err != nil || len(hours) == 0 {
		return nil
	}
	return hours
}
