package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type canonicalPOIRepository interface {
	FindPoiByNameAndCity(context.Context, string, uuid.UUID) (*locitypes.POIDetailedInfo, error)
	SavePoi(context.Context, locitypes.POIDetailedInfo, uuid.UUID) (uuid.UUID, error)
}

// canonicalizePOIs links generated results to the canonical PostGIS-backed POI
// row before they are streamed or copied into a trip. The maintenance worker
// backfills embeddings separately so provider latency never blocks the stream.
// Individual row failures are best-effort: one bad place must not make the whole
// recommendation stream disappear.
func (l *ServiceImpl) canonicalizePOIs(
	ctx context.Context,
	pois []locitypes.POIDetailedInfo,
	cityID uuid.UUID,
) []locitypes.POIDetailedInfo {
	return canonicalizePOIsWithDependencies(ctx, pois, cityID, l.poiRepo, l.logger)
}

func canonicalizePOIsWithDependencies(
	ctx context.Context,
	pois []locitypes.POIDetailedInfo,
	cityID uuid.UUID,
	repo canonicalPOIRepository,
	logger *slog.Logger,
) []locitypes.POIDetailedInfo {
	if cityID == uuid.Nil || len(pois) == 0 || repo == nil {
		return pois
	}
	if logger == nil {
		logger = slog.Default()
	}

	result := append([]locitypes.POIDetailedInfo(nil), pois...)
	for index := range result {
		candidate := &result[index]
		candidate.CityID = cityID
		if candidate.Name == "" {
			continue
		}

		existing, err := repo.FindPoiByNameAndCity(ctx, candidate.Name, cityID)
		if err != nil {
			logger.WarnContext(ctx, "failed to resolve canonical POI",
				slog.String("poi_name", candidate.Name), slog.Any("error", err))
			continue
		}
		if existing != nil && existing.ID != uuid.Nil {
			candidate.ID = existing.ID
			continue
		}

		toSave := *candidate
		if toSave.DescriptionPOI == "" {
			toSave.DescriptionPOI = toSave.Description
		}
		poiID, err := repo.SavePoi(ctx, toSave, cityID)
		if err != nil {
			logger.WarnContext(ctx, "failed to create canonical POI",
				slog.String("poi_name", candidate.Name), slog.Any("error", err))
			continue
		}
		candidate.ID = poiID
	}
	return result
}
