package poi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// discoveryQuery is what we ask the model for when a city has nothing stored.
// Deliberately generic: this is cold-start repair, not a search.
const discoveryQuery = "top things to do"

// DiscoverPOIsForCity fills in a city that has no places yet.
//
// It exists for cities the resolver has just created. Those are real places with
// real coordinates, but nothing has ever generated content for them, so a
// comparison column shows weather and a drive time and no reason to go. One
// generation fixes that permanently, because the results are persisted and
// embedded like any other.
//
// It takes the city id rather than only a name because the caller already holds
// the row these places belong to. Going through the name-resolving path instead
// could attach them to a different row than the one the caller reads back.
func (s *ServiceImpl) DiscoverPOIsForCity(ctx context.Context, cityID uuid.UUID, cityName string) error {
	if cityID == uuid.Nil {
		return fmt.Errorf("discover POIs: no city id for %q", cityName)
	}
	if cityName == "" {
		return fmt.Errorf("discover POIs: no city name for %s", cityID)
	}

	pois, err := s.generatePOIsWithLLM(ctx, discoveryQuery, cityName)
	if err != nil {
		return fmt.Errorf("discover POIs for %q: %w", cityName, err)
	}
	if len(pois) == 0 {
		s.logger.WarnContext(ctx, "discovery produced no places for city",
			slog.String("city", cityName), slog.String("city_id", cityID.String()))
		return nil
	}

	s.persistGeneratedPOIsForCity(ctx, pois, cityID, cityName)
	return nil
}
