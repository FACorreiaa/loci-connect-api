package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// poiImageRepository is the slice of the POI repository this file needs.
// Narrow on purpose, like canonicalPOIRepository above it.
type poiImageRepository interface {
	ImagesForPOIs(ctx context.Context, poiIDs []uuid.UUID) (map[uuid.UUID][]locitypes.POIImage, error)
}

// attachImages fills in pictures for every place that resolved to a real row.
//
// Runs after canonicalizePOIs, because only then does a generated place carry
// the id of the row its pictures hang off. A place the model invented has no
// id, so it has no pictures — which is the same rule as its map link.
//
// One query for the whole answer rather than one per list: an itinerary plus
// its city POIs is twenty-odd places, and twenty round trips to decorate
// thumbnails would cost more than generating the text did.
//
// Best-effort: an answer without pictures is worth sending, so a failure here
// is logged and the turn continues.
func (l *ServiceImpl) attachImages(ctx context.Context, data *locitypes.AiCityResponse) {
	if data == nil || l.poiRepo == nil {
		return
	}
	attachImagesWithDependencies(ctx, data, l.poiRepo, l.logger)
}

func attachImagesWithDependencies(
	ctx context.Context,
	data *locitypes.AiCityResponse,
	repo poiImageRepository,
	logger *slog.Logger,
) {
	if data == nil || repo == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	lists := [][]locitypes.POIDetailedInfo{
		data.PointsOfInterest,
		data.AIItineraryResponse.PointsOfInterest,
		data.AIItineraryResponse.Restaurants,
		data.AIItineraryResponse.Bars,
		data.Activities,
	}

	seen := make(map[uuid.UUID]struct{})
	var ids []uuid.UUID
	for _, list := range lists {
		for i := range list {
			id := list[i].ID
			if id == uuid.Nil {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}

	byPOI, err := repo.ImagesForPOIs(ctx, ids)
	if err != nil {
		logger.WarnContext(ctx, "could not attach POI images",
			slog.Int("poi_count", len(ids)), slog.Any("error", err))
		return
	}
	if len(byPOI) == 0 {
		return
	}

	for _, list := range lists {
		for i := range list {
			images := byPOI[list[i].ID]
			if len(images) == 0 {
				continue
			}
			// Both forms: ImageCredits is what a surface must render, Images is
			// what the existing clients already read. They describe the same
			// pictures in the same order.
			list[i].ImageCredits = images
			urls := make([]string, 0, len(images))
			for _, img := range images {
				urls = append(urls, img.URL)
			}
			list[i].Images = urls
		}
	}
}
