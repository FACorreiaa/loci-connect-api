package assistant

import (
	"context"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/memory"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/poi"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/social"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type (
	nilLists    struct{}
	nilPOIs     struct{}
	nilMemory   struct{}
	nilFriends  struct{}
	nilVisits   struct{}
	nilActivity struct{}
)

func (nilLists) GetUserLists(context.Context, uuid.UUID, bool) ([]*locitypes.List, error) {
	return nil, nil
}

func (nilLists) GetListDetails(context.Context, uuid.UUID, uuid.UUID) (*locitypes.ListWithItems, error) {
	return nil, nil
}

func (nilPOIs) GetFavouritePOIsByUserID(context.Context, uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	return nil, nil
}

func (nilPOIs) GetPOI(context.Context, uuid.UUID) (*locitypes.POIDetailedInfo, error) {
	return nil, nil
}

func (nilPOIs) SearchPOIs(context.Context, locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error) {
	return nil, nil
}

func (nilPOIs) SearchPOIsFused(context.Context, locitypes.POIFilter, string, float64) ([]poi.SearchResult, error) {
	return nil, nil
}

func (nilMemory) Get(context.Context, uuid.UUID, bool) (*memory.Profile, error) { return nil, nil }

func (nilFriends) ListFriends(context.Context, uuid.UUID) ([]social.Friend, error) { return nil, nil }

func (nilVisits) ListVisitedCities(context.Context, uuid.UUID, int, int) ([]*travelhistory.VisitedCity, int, error) {
	return nil, 0, nil
}

func (nilActivity) GetUserActivityFeed(context.Context, uuid.UUID, int, int, locitypes.ActivityFeedFilter) ([]locitypes.ActivityEntry, bool, error) {
	return nil, false, nil
}
