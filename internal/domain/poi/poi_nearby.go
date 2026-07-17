package poi

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (s *ServiceImpl) GetGeneralPOIByDistance(ctx context.Context, userID uuid.UUID, lat, lon, distance float64) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "GetGeneralPOIByDistance")
	defer span.End()

	cacheKey := generateFilteredPOICacheKey(lat, lon, distance, userID)
	span.SetAttributes(attribute.String("cache.key", cacheKey))

	if cached, found := s.cache.Get(cacheKey); found {
		if pois, ok := cached.([]locitypes.POIDetailedInfo); ok {
			s.logger.InfoContext(ctx, "Serving POIs from cache", "key", cacheKey)
			return clonePOISlice(pois), nil
		}
	}

	s.logger.InfoContext(ctx, "Cache miss. Querying POIs from database.", "lat", lat, "lon", lon, "distance_m", distance)
	poisFromDB, err := s.poiRepository.GetPOIsByLocationAndDistance(ctx, lat, lon, distance)
	if err == nil && len(poisFromDB) > 0 {
		for i := range poisFromDB {
			poisFromDB[i].Source = "points_of_interest"
		}
		cachedPOIs := clonePOISlice(poisFromDB)
		s.cache.Set(cacheKey, cachedPOIs, cachestore.DefaultGeoTTL)
		return clonePOISlice(cachedPOIs), nil
	}

	s.logger.InfoContext(ctx, "No POIs found in database, falling back to LLM generation")
	span.AddEvent("database_miss_fallback_to_llm")

	// generateAndEnrichPOIs handles the re-prompt loop and returns an empty
	// (non-nil-error) slice when the model produces nothing usable, so an
	// empty result is surfaced honestly instead of masked as success.
	enrichedPOIs, err := s.generateAndEnrichPOIs(ctx, userID, lat, lon, distance)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	cachedPOIs := clonePOISlice(enrichedPOIs)
	s.cache.Set(cacheKey, cachedPOIs, cachestore.DefaultGeoTTL)
	span.SetStatus(codes.Ok, "POIs generated via LLM and cached")
	return clonePOISlice(cachedPOIs), nil
}

func (s *ServiceImpl) generatePOIsFromLLM(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, strict bool) (*locitypes.GenAIResponse, error) {
	resultCh := make(chan locitypes.GenAIResponse, 1)
	// A higher temperature on the strict retry nudges the model off a previous
	// degenerate (null/empty) completion.
	temperature := float32(0.7)
	if strict {
		temperature = 0.9
	}
	var wg sync.WaitGroup
	concurrency.Go(&wg, s.logger, func() {
		s.getGeneralPOIByDistance(ctx, userID, lat, lon, distance, strict, resultCh, &genai.GenerateContentConfig{
			Temperature:     genai.Ptr[float32](temperature),
			MaxOutputTokens: 16384,
		})
	})
	wg.Wait()
	close(resultCh)

	result := <-resultCh
	if result.Err != nil {
		return nil, result.Err
	}
	return &result, nil
}

// GetNearbyRestaurants get nearby restaurants with optional filters
func (s *ServiceImpl) GetNearbyRestaurants(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, cuisineType, priceRange string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "GetNearbyRestaurants", trace.WithAttributes(
		attribute.Float64("location.lat", lat),
		attribute.Float64("location.lon", lon),
		attribute.Float64("distance", distance),
		attribute.String("cuisine_type", cuisineType),
		attribute.String("price_range", priceRange),
	))
	defer span.End()

	// Build cache key with domain-specific filters
	cacheKey := fmt.Sprintf("restaurants_%f_%f_%f_%s_%s_%s", lat, lon, distance, userID.String(), cuisineType, priceRange)

	if cached, found := s.cache.Get(cacheKey); found {
		if pois, ok := cached.([]locitypes.POIDetailedInfo); ok {
			s.logger.InfoContext(ctx, "Serving restaurants from cache", "key", cacheKey)
			return pois, nil
		}
	}

	s.logger.InfoContext(ctx, "Querying restaurants from database", "lat", lat, "lon", lon, "distance", distance)

	// Get restaurants from database with filters
	restaurants, err := s.poiRepository.GetPOIsByLocationAndDistanceWithCategory(ctx, lat, lon, distance, "restaurant")
	if err == nil && len(restaurants) > 0 {
		// Apply domain-specific filters
		filteredRestaurants := s.filterRestaurants(restaurants, cuisineType, priceRange)

		// Mark as database source
		for i := range filteredRestaurants {
			filteredRestaurants[i].Source = "points_of_interest"
		}

		s.cache.Set(cacheKey, filteredRestaurants, cachestore.DefaultGeoTTL)
		return filteredRestaurants, nil
	}

	s.logger.InfoContext(ctx, "No restaurants found in database, falling back to LLM generation")

	// Generate restaurants using LLM with domain-specific prompt
	enrichedRestaurants, err := s.enrichLLMWithRetry(ctx, lat, lon, distance, "restaurants",
		func() (*locitypes.GenAIResponse, error) {
			return s.generateRestaurantsFromLLM(ctx, userID, lat, lon, distance, cuisineType, priceRange)
		})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	s.cache.Set(cacheKey, enrichedRestaurants, cachestore.DefaultGeoTTL)
	return enrichedRestaurants, nil
}

// GetNearbyActivities get nearby activities with optional filters
func (s *ServiceImpl) GetNearbyActivities(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, activityType, duration string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "GetNearbyActivities", trace.WithAttributes(
		attribute.Float64("location.lat", lat),
		attribute.Float64("location.lon", lon),
		attribute.Float64("distance", distance),
		attribute.String("activity_type", activityType),
		attribute.String("duration", duration),
	))
	defer span.End()

	// Build cache key with domain-specific filters
	cacheKey := fmt.Sprintf("activities_%f_%f_%f_%s_%s_%s", lat, lon, distance, userID.String(), activityType, duration)

	if cached, found := s.cache.Get(cacheKey); found {
		if pois, ok := cached.([]locitypes.POIDetailedInfo); ok {
			s.logger.InfoContext(ctx, "Serving activities from cache", "key", cacheKey)
			return pois, nil
		}
	}

	s.logger.InfoContext(ctx, "Querying activities from database", "lat", lat, "lon", lon, "distance", distance)

	// Get activities from database with filters
	activities, err := s.poiRepository.GetPOIsByLocationAndDistanceWithCategory(ctx, lat, lon, distance, "activity")
	if err == nil && len(activities) > 0 {
		// Apply domain-specific filters
		filteredActivities := s.filterActivities(activities, activityType, duration)

		// Mark as database source
		for i := range filteredActivities {
			filteredActivities[i].Source = "points_of_interest"
		}

		s.cache.Set(cacheKey, filteredActivities, cachestore.DefaultGeoTTL)
		return filteredActivities, nil
	}

	s.logger.InfoContext(ctx, "No activities found in database, falling back to LLM generation")

	// Generate activities using LLM with domain-specific prompt
	enrichedActivities, err := s.enrichLLMWithRetry(ctx, lat, lon, distance, "activities",
		func() (*locitypes.GenAIResponse, error) {
			return s.generateActivitiesFromLLM(ctx, userID, lat, lon, distance, activityType, duration)
		})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	s.cache.Set(cacheKey, enrichedActivities, cachestore.DefaultGeoTTL)
	return enrichedActivities, nil
}

// GetNearbyHotels get nearby hotels with optional filters
func (s *ServiceImpl) GetNearbyHotels(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, starRating, amenities string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "GetNearbyHotels", trace.WithAttributes(
		attribute.Float64("location.lat", lat),
		attribute.Float64("location.lon", lon),
		attribute.Float64("distance", distance),
		attribute.String("star_rating", starRating),
		attribute.String("amenities", amenities),
	))
	defer span.End()

	// Build cache key with domain-specific filters
	cacheKey := fmt.Sprintf("hotels_%f_%f_%f_%s_%s_%s", lat, lon, distance, userID.String(), starRating, amenities)

	if cached, found := s.cache.Get(cacheKey); found {
		if pois, ok := cached.([]locitypes.POIDetailedInfo); ok {
			s.logger.InfoContext(ctx, "Serving hotels from cache", "key", cacheKey)
			return pois, nil
		}
	}

	s.logger.InfoContext(ctx, "Querying hotels from database", "lat", lat, "lon", lon, "distance", distance)

	// Get hotels from database with filters
	hotels, err := s.poiRepository.GetPOIsByLocationAndDistanceWithCategory(ctx, lat, lon, distance, "hotel")
	if err == nil && len(hotels) > 0 {
		// Apply domain-specific filters
		filteredHotels := s.filterHotels(hotels, starRating, amenities)

		// Mark as database source
		for i := range filteredHotels {
			filteredHotels[i].Source = "points_of_interest"
		}

		s.cache.Set(cacheKey, filteredHotels, cachestore.DefaultGeoTTL)
		return filteredHotels, nil
	}

	s.logger.InfoContext(ctx, "No hotels found in database, falling back to LLM generation")

	// Generate hotels using LLM with domain-specific prompt
	enrichedHotels, err := s.enrichLLMWithRetry(ctx, lat, lon, distance, "hotels",
		func() (*locitypes.GenAIResponse, error) {
			return s.generateHotelsFromLLM(ctx, userID, lat, lon, distance, starRating, amenities)
		})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	s.cache.Set(cacheKey, enrichedHotels, cachestore.DefaultGeoTTL)
	return enrichedHotels, nil
}

// GetNearbyAttractions get nearby attractions with optional filters
func (s *ServiceImpl) GetNearbyAttractions(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, attractionType, isOutdoor string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "GetNearbyAttractions", trace.WithAttributes(
		attribute.Float64("location.lat", lat),
		attribute.Float64("location.lon", lon),
		attribute.Float64("distance", distance),
		attribute.String("attraction_type", attractionType),
		attribute.String("is_outdoor", isOutdoor),
	))
	defer span.End()

	// Build cache key with domain-specific filters
	cacheKey := fmt.Sprintf("attractions_%f_%f_%f_%s_%s_%s", lat, lon, distance, userID.String(), attractionType, isOutdoor)

	if cached, found := s.cache.Get(cacheKey); found {
		if pois, ok := cached.([]locitypes.POIDetailedInfo); ok {
			s.logger.InfoContext(ctx, "Serving attractions from cache", "key", cacheKey)
			return pois, nil
		}
	}

	s.logger.InfoContext(ctx, "Querying attractions from database", "lat", lat, "lon", lon, "distance", distance)

	// Get attractions from database with filters
	attractions, err := s.poiRepository.GetPOIsByLocationAndDistanceWithCategory(ctx, lat, lon, distance, "attraction")
	if err == nil && len(attractions) > 0 {
		// Apply domain-specific filters
		filteredAttractions := s.filterAttractions(attractions, attractionType, isOutdoor)

		// Mark as database source
		for i := range filteredAttractions {
			filteredAttractions[i].Source = "points_of_interest"
		}

		s.cache.Set(cacheKey, filteredAttractions, cachestore.DefaultGeoTTL)
		return filteredAttractions, nil
	}

	s.logger.InfoContext(ctx, "No attractions found in database, falling back to LLM generation")

	// Generate attractions using LLM with domain-specific prompt
	enrichedAttractions, err := s.enrichLLMWithRetry(ctx, lat, lon, distance, "attractions",
		func() (*locitypes.GenAIResponse, error) {
			return s.generateAttractionsFromLLM(ctx, userID, lat, lon, distance, attractionType, isOutdoor)
		})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	s.cache.Set(cacheKey, enrichedAttractions, cachestore.DefaultGeoTTL)
	return enrichedAttractions, nil
}

// Helper functions for domain-specific filtering
func (s *ServiceImpl) filterRestaurants(restaurants []locitypes.POIDetailedInfo, cuisineType, priceRange string) []locitypes.POIDetailedInfo {
	if cuisineType == "" && priceRange == "" {
		return restaurants
	}

	filtered := make([]locitypes.POIDetailedInfo, 0)
	for _, restaurant := range restaurants {
		// Filter by cuisine type
		if cuisineType != "" && restaurant.Category != cuisineType {
			continue
		}
		// Filter by price range
		if priceRange != "" && restaurant.PriceLevel != priceRange {
			continue
		}
		filtered = append(filtered, restaurant)
	}
	return filtered
}

func (s *ServiceImpl) filterActivities(activities []locitypes.POIDetailedInfo, activityType, duration string) []locitypes.POIDetailedInfo {
	if activityType == "" && duration == "" {
		return activities
	}

	filtered := make([]locitypes.POIDetailedInfo, 0)
	for _, activity := range activities {
		// Filter by activity type
		if activityType != "" && activity.Category != activityType {
			continue
		}
		// Filter by duration (using description as proxy for duration since TimeToSpend field doesn't exist)
		if duration != "" && !strings.Contains(strings.ToLower(activity.Description), strings.ToLower(duration)) {
			continue
		}
		filtered = append(filtered, activity)
	}
	return filtered
}

func (s *ServiceImpl) filterHotels(hotels []locitypes.POIDetailedInfo, starRating, amenities string) []locitypes.POIDetailedInfo {
	if starRating == "" && amenities == "" {
		return hotels
	}

	filtered := make([]locitypes.POIDetailedInfo, 0)
	for _, hotel := range hotels {
		// Filter by star rating
		if starRating != "" && hotel.PriceLevel != starRating {
			continue
		}
		// Filter by amenities (basic string matching)
		if amenities != "" {
			if !strings.Contains(strings.ToLower(hotel.Amenities), strings.ToLower(amenities)) {
				continue
			}
		}
		filtered = append(filtered, hotel)
	}
	return filtered
}

func (s *ServiceImpl) filterAttractions(attractions []locitypes.POIDetailedInfo, attractionType, isOutdoor string) []locitypes.POIDetailedInfo {
	if attractionType == "" && isOutdoor == "" {
		return attractions
	}

	filtered := make([]locitypes.POIDetailedInfo, 0)
	for _, attraction := range attractions {
		// Filter by attraction type
		if attractionType != "" && attraction.Category != attractionType {
			continue
		}
		// Filter by outdoor/indoor (basic tag matching)
		if isOutdoor != "" {
			hasOutdoorTag := false
			for _, tag := range attraction.Tags {
				if (isOutdoor == "true" && tag == "outdoor") || (isOutdoor == "false" && tag == "indoor") {
					hasOutdoorTag = true
					break
				}
			}
			if !hasOutdoorTag {
				continue
			}
		}
		filtered = append(filtered, attraction)
	}
	return filtered
}

// TODO
// generateRestaurantsFromLLM
func (s *ServiceImpl) generateRestaurantsFromLLM(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, _, _ string) (*locitypes.GenAIResponse, error) {
	resultCh := make(chan locitypes.GenAIResponse, 1)
	// func (s *ServiceImpl) generateRestaurantsFromgenerativeAI...
	var wg sync.WaitGroup
	concurrency.Go(&wg, s.logger, func() {
		s.getGeneralRestaurantByDistance(ctx, userID, lat, lon, distance, resultCh, &genai.GenerateContentConfig{
			Temperature:     genai.Ptr[float32](0.7),
			MaxOutputTokens: 16384,
		})
	})
	wg.Wait()
	close(resultCh)

	result := <-resultCh
	if result.Err != nil {
		return nil, result.Err
	}
	return &result, nil
}

func (s *ServiceImpl) generateActivitiesFromLLM(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, _, _ string) (*locitypes.GenAIResponse, error) {
	resultCh := make(chan locitypes.GenAIResponse, 1)
	// func (s *ServiceImpl) generateActivitiesFromgenerativeAI...
	var wg sync.WaitGroup
	concurrency.Go(&wg, s.logger, func() {
		s.getGeneralActivitiesByDistance(ctx, userID, lat, lon, distance, resultCh, &genai.GenerateContentConfig{
			Temperature:     genai.Ptr[float32](0.7),
			MaxOutputTokens: 16384,
		})
	})
	wg.Wait()
	close(resultCh)

	result := <-resultCh
	if result.Err != nil {
		return nil, result.Err
	}
	return &result, nil
}

func (s *ServiceImpl) generateHotelsFromLLM(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, _, _ string) (*locitypes.GenAIResponse, error) {
	resultCh := make(chan locitypes.GenAIResponse, 1)
	// func (s *ServiceImpl) generateHotelsFromgenerativeAI...
	var wg sync.WaitGroup
	concurrency.Go(&wg, s.logger, func() {
		s.getGeneralHotelsByDistance(ctx, userID, lat, lon, distance, resultCh, &genai.GenerateContentConfig{
			Temperature:     genai.Ptr[float32](0.7),
			MaxOutputTokens: 16384,
		})
	})
	wg.Wait()
	close(resultCh)

	result := <-resultCh
	if result.Err != nil {
		return nil, result.Err
	}
	return &result, nil
}

func (s *ServiceImpl) generateAttractionsFromLLM(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, _, _ string) (*locitypes.GenAIResponse, error) {
	resultCh := make(chan locitypes.GenAIResponse, 1)
	// func (s *ServiceImpl) generateAttractionsFromgenerativeAI...
	var wg sync.WaitGroup
	concurrency.Go(&wg, s.logger, func() {
		s.getGeneralAttractionsByDistance(ctx, userID, lat, lon, distance, resultCh, &genai.GenerateContentConfig{
			Temperature:     genai.Ptr[float32](0.7),
			MaxOutputTokens: 16384,
		})
	})
	wg.Wait()
	close(resultCh)

	result := <-resultCh
	if result.Err != nil {
		return nil, result.Err
	}
	return &result, nil
}
