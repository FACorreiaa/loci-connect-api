package poi

import (
	"context"
	"fmt"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

func (s *ServiceImpl) AddPoiToFavourites(ctx context.Context, userID, poiID uuid.UUID, isLLMGenerated bool) (uuid.UUID, error) {
	var id uuid.UUID
	if !isLLMGenerated {

		id, err := s.poiRepository.AddPoiToFavourites(ctx, userID, poiID)
		if err != nil {
			s.logger.Error("failed to add POI to favourites", "error", err)
			return uuid.Nil, err
		}
		return id, nil
	}

	id, err := s.poiRepository.AddLLMPoiToFavourite(ctx, userID, poiID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to insert favorite LLM POI: %w", err)
	}

	return id, nil
}

func (s *ServiceImpl) RemovePoiFromFavourites(ctx context.Context, userID, poiID uuid.UUID, isLLMGenerated bool) error {
	if isLLMGenerated {
		err := s.poiRepository.RemoveLLMPoiFromFavourite(ctx, userID, poiID)
		if err != nil {
			s.logger.Error("failed to remove LLM POI from favourites", "error", err)
			return err
		}
	} else {
		err := s.poiRepository.RemovePoiFromFavourites(ctx, userID, poiID)
		if err != nil {
			s.logger.Error("failed to remove POI from favourites", "error", err)
			return err
		}
	}
	return nil
}

func (s *ServiceImpl) GetFavouritePOIsByUserID(ctx context.Context, userID uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	pois, err := s.poiRepository.GetFavouritePOIsByUserID(ctx, userID)
	if err != nil {
		s.logger.Error("failed to get favourite POIs by user ID", "error", err)
		return nil, err
	}
	return pois, nil
}

func (s *ServiceImpl) GetFavouritePOIsByUserIDPaginated(ctx context.Context, userID uuid.UUID, limit, offset int) ([]locitypes.POIDetailedInfo, int, error) {
	pois, total, err := s.poiRepository.GetFavouritePOIsByUserIDPaginated(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("failed to get paginated favourite POIs by user ID", "error", err)
		return nil, 0, err
	}
	return pois, total, nil
}
