package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
	"github.com/FACorreiaa/loci-connect-api/pkg/concurrency"
)

// getPOIDetailedInfos returns a formatted string with POI details.
func (l *ServiceImpl) getPOIDetailedInfos(ctx context.Context,
	city string, lat, lon float64, userID uuid.UUID,
	resultCh chan<- locitypes.POIDetailedInfo, config *genai.GenerateContentConfig,
) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "getPOIDetailedInfos", trace.WithAttributes(
		attribute.String("city.name", city),
		attribute.Float64("latitude", lat),
		attribute.Float64("longitude", lon),
	))
	defer span.End()

	if city == "" || lat == 0 || lon == 0 {
		return
	}

	startTime := time.Now()

	prompt := getPOIDetailsPrompt(city, lat, lon)
	span.SetAttributes(attribute.Int("prompt.length", len(prompt)))
	response, err := l.aiClient.Generate(ctx, prompt, config)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to generate POI details")
		resultCh <- locitypes.POIDetailedInfo{Err: fmt.Errorf("failed to generate POI details: %w", err)}
		return
	}

	var txt string
	for _, candidate := range response.Candidates {
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			txt = candidate.Content.Parts[0].Text
			break
		}
	}
	if txt == "" {
		err := fmt.Errorf("no valid POI details content from AI")
		span.RecordError(err)
		span.SetStatus(codes.Error, "Empty response from AI")
		resultCh <- locitypes.POIDetailedInfo{Err: err}
		return
	}

	span.SetAttributes(attribute.Int("response.length", len(txt)))
	cleanTxt := generativeAI.CleanJSON(txt)
	var detailedInfo locitypes.POIDetailedInfo
	if err := json.Unmarshal([]byte(cleanTxt), &detailedInfo); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to parse POI details JSON")
		resultCh <- locitypes.POIDetailedInfo{Err: fmt.Errorf("failed to parse POI details JSON: %w", err)}
		return
	}
	latencyMs := int(time.Since(startTime).Milliseconds())
	span.SetAttributes(attribute.Int("response.latency_ms", latencyMs))
	span.SetStatus(codes.Ok, "POI details generated successfully")
	interaction := locitypes.LlmInteraction{
		UserID:       userID,
		Prompt:       prompt,
		ResponseText: txt,
		ModelUsed:    l.model, // Adjust based on your AI client
		LatencyMs:    latencyMs,
		CityName:     city,
		// request payload
		// response payload
		// Add token counts if available from response (depends on genai API)
		// PromptTokens, CompletionTokens, TotalTokens
		// RequestPayload, ResponsePayload if you serialize the full request/response
	}

	savedInteractionID, err := l.llmInteractionRepo.SaveInteraction(ctx, interaction)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to save LLM interaction for POI details")
		resultCh <- locitypes.POIDetailedInfo{Err: fmt.Errorf("failed to save LLM interaction for POI details: %w", err)}
		return
	}
	resultCh <- locitypes.POIDetailedInfo{
		City:         city,
		Name:         detailedInfo.Name,
		Latitude:     detailedInfo.Latitude,
		Longitude:    detailedInfo.Longitude,
		Description:  detailedInfo.Description,
		Address:      detailedInfo.Address,
		OpeningHours: detailedInfo.OpeningHours,
		PhoneNumber:  detailedInfo.PhoneNumber,
		Website:      detailedInfo.Website,
		Rating:       detailedInfo.Rating,
		Tags:         detailedInfo.Tags,
		Images:       detailedInfo.Images,
		PriceRange:   detailedInfo.PriceRange,
		Err:          nil,
		// Include the saved interaction ID for tracking

		LlmInteractionID: savedInteractionID,
	}
	span.SetAttributes(attribute.String("llm_interaction.id", savedInteractionID.String()))
	span.SetStatus(codes.Ok, "POI details generated and saved successfully")
}

func (l *ServiceImpl) GetPOIDetailedInfosResponse(ctx context.Context, userID uuid.UUID, city string, lat, lon float64) (*locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "GetPOIDetailedInfosResponse", trace.WithAttributes(
		attribute.String("city.name", city),
		attribute.Float64("latitude", lat),
		attribute.Float64("longitude", lon),
		attribute.String("user.id", userID.String()),
	))
	defer span.End()

	l.logger.DebugContext(ctx, "Starting POI details generation",
		slog.String("city", city), slog.Float64("latitude", lat), slog.Float64("longitude", lon), slog.String("userID", userID.String()))

	// Generate cache key
	cacheKey := generatePOICacheKey(city, lat, lon, 0.0, userID)
	span.SetAttributes(attribute.String("cache.key", cacheKey))

	// Check cache
	if cached, found := l.cache.Get(cacheKey); found {
		if p, ok := cached.(*locitypes.POIDetailedInfo); ok {
			l.logger.InfoContext(ctx, "Cache hit for POI details", slog.String("cache_key", cacheKey))
			span.AddEvent("Cache hit")
			span.SetStatus(codes.Ok, "POI details served from cache")
			copy := *p
			return &copy, nil
		}
	}

	// Find city ID
	cityData, err := l.cityRepo.FindCityByNameAndCountry(ctx, city, "") // Adjust country if needed
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to find city", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to find city: %w", err)
	}
	if cityData == nil {
		l.logger.WarnContext(ctx, "City not found", slog.String("city", city))
		span.SetStatus(codes.Error, "City not found")
		return nil, fmt.Errorf("city %s not found", city)
	}
	cityID := cityData.ID

	p, err := l.poiRepo.FindPOIDetails(ctx, cityID, lat, lon, 100.0) // 100m tolerance
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to query POI details from database", slog.Any("error", err))
		span.RecordError(err)
		return nil, fmt.Errorf("failed to query POI details: %w", err)
	}
	if p != nil {
		p.City = city
		cached := *p
		l.cache.Set(cacheKey, &cached, cachestore.DefaultGeoTTL)
		l.logger.InfoContext(ctx, "Database hit for POI details", slog.String("cache_key", cacheKey))
		span.AddEvent("Database hit")
		span.SetStatus(codes.Ok, "POI details served from database")
		return p, nil
	}

	// Cache and database miss: fetch from Gemini API
	l.logger.DebugContext(ctx, "Cache and database miss, fetching POI details from AI", slog.String("cache_key", cacheKey))
	span.AddEvent("Cache and database miss")

	resultCh := make(chan locitypes.POIDetailedInfo, 1)
	var wg sync.WaitGroup

	concurrency.Go(&wg, l.logger, func() {
		l.getPOIDetailedInfos(ctx, city, lat, lon, userID, resultCh, &genai.GenerateContentConfig{Temperature: genai.Ptr[float32](defaultTemperature)})
	})

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	res, ok := <-resultCh
	if !ok {
		l.logger.WarnContext(ctx, "No response received for POI details")
		span.SetStatus(codes.Error, "No response received")
		return nil, fmt.Errorf("no response received for POI details")
	}

	if res.Err != nil {
		l.logger.ErrorContext(ctx, "Error generating POI details", slog.Any("error", res.Err))
		span.RecordError(res.Err)
		span.SetStatus(codes.Error, "Failed to generate POI details")
		return nil, res.Err
	}

	poiResult := &res

	// Save to database
	_, err = l.poiRepo.SavePoi(ctx, *poiResult, cityID)
	if err != nil {
		l.logger.WarnContext(ctx, "Failed to save POI details to database", slog.Any("error", err))
		span.RecordError(err)
		// Continue despite error to avoid blocking user
	}

	// Store in cache
	cachedResult := *poiResult
	l.cache.Set(cacheKey, &cachedResult, cachestore.DefaultGeoTTL)
	l.logger.DebugContext(ctx, "Stored POI details in cache", slog.String("cache_key", cacheKey))
	span.AddEvent("Stored in cache")

	span.SetStatus(codes.Ok, "POI details generated and cached successfully")
	return poiResult, nil
}

// generatePOIData queries the LLM for POI details and calculates distance using PostGIS
func (l *ServiceImpl) generatePOIData(ctx context.Context, poiName, cityName string, userLocation *locitypes.UserLocation, userID, cityID uuid.UUID) (locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "GeneratePOIData", trace.WithAttributes(
		attribute.String("p.name", poiName),
		attribute.String("city.name", cityName),
	))
	defer span.End()

	// Create a prompt for the LLM
	prompt := generatedContinuedConversationPrompt(poiName, cityName)

	// Generate LLM response
	response, err := l.aiClient.GenerateText(ctx, prompt, nil)
	if err != nil {
		span.RecordError(err)
		return locitypes.POIDetailedInfo{}, fmt.Errorf("failed to generate POI data: %w", err)
	}

	interaction := locitypes.LlmInteraction{
		UserID:       userID,
		Prompt:       prompt,
		ResponseText: response,
		ModelUsed:    l.model,
		CityName:     cityName,
	}
	savedLlmInteractionID, err := l.llmInteractionRepo.SaveInteraction(ctx, interaction)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to save LLM interaction in generatePOIData", slog.Any("error", err))
		// Decide if this is fatal for POI generation. It might be if FK is NOT NULL.
		return locitypes.POIDetailedInfo{}, fmt.Errorf("failed to save LLM interaction: %w", err)
	}
	span.SetAttributes(attribute.String("generativeAI.interaction_id.for_poi_data", savedLlmInteractionID.String()))

	cleanResponse := generativeAI.CleanJSON(response)
	var poiData locitypes.POIDetailedInfo
	if err := json.Unmarshal([]byte(cleanResponse), &poiData); err != nil || poiData.Name == "" {
		l.logger.WarnContext(ctx, "LLM returned invalid or empty POI data",
			slog.String("poiName", poiName),
			slog.String("llmResponse", response),
			slog.Any("unmarshalError", err))
		span.AddEvent("Invalid LLM response")
		poiData = locitypes.POIDetailedInfo{
			ID:             uuid.New(),
			Name:           poiName,
			Latitude:       0,
			Longitude:      0,
			Category:       "Attraction",
			DescriptionPOI: fmt.Sprintf("Added %s based on user request, but detailed data not available.", poiName),
			Distance:       0,
		}
	}
	if poiData.ID == uuid.Nil { // Assign an ID if LLM didn't provide one
		poiData.ID = uuid.New()
	}
	poiData.LlmInteractionID = savedLlmInteractionID

	// Calculate distance if coordinates are valid
	if userLocation != nil && userLocation.UserLat != 0 && userLocation.UserLon != 0 && poiData.Latitude != 0 && poiData.Longitude != 0 {
		distance, err := l.poiRepo.CalculateDistancePostGIS(ctx, userLocation.UserLat, userLocation.UserLon, poiData.Latitude, poiData.Longitude)
		if err != nil {
			l.logger.WarnContext(ctx, "Failed to calculate distance", slog.Any("error", err))
			span.RecordError(err)
			poiData.Distance = 0
		} else {
			poiData.Distance = distance
			span.SetAttributes(attribute.Float64("p.distance_meters", distance))
			l.logger.DebugContext(ctx, "Calculated distance for POI",
				slog.String("poiName", poiName),
				slog.Float64("distance_meters", distance))
		}
	} else {
		poiData.Distance = 0
		span.AddEvent("Distance not calculated due to missing location data")
		l.logger.WarnContext(ctx, "Cannot calculate distance",
			slog.Bool("userLocationAvailable", userLocation != nil),
			slog.Float64("userLat", userLocation.UserLat),
			slog.Float64("userLon", userLocation.UserLon),
			slog.Float64("poiLatitude", poiData.Latitude),
			slog.Float64("poiLongitude", poiData.Longitude))
	}

	// Save POI to database
	llmInteractionID := uuid.New()
	_, err = l.llmInteractionRepo.SaveSinglePOI(ctx, poiData, userID, cityID, savedLlmInteractionID)
	if err != nil {
		l.logger.WarnContext(ctx, "Failed to save POI to database", slog.Any("error", err))
		span.RecordError(err)
	}

	span.SetAttributes(
		attribute.String("p.name", poiData.Name),
		attribute.Float64("p.latitude", poiData.Latitude),
		attribute.Float64("p.longitude", poiData.Longitude),
		attribute.String("p.category", poiData.Category),
		attribute.String("llm_interaction.id", llmInteractionID.String()),
	)
	return poiData, nil
}

// enhancePOIRecommendationsWithSemantics uses embeddings to find similar POIs and enrich recommendations
//func (l *ServiceImpl) enhancePOIRecommendationsWithSemantics(ctx context.Context, userMessage string, cityID uuid.UUID, userPreferences []string, limit int) ([]locitypes.POIDetailedInfo, error) {
//	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "enhancePOIRecommendationsWithSemantics", trace.WithAttributes(
//		attribute.String("user.message", userMessage),
//		attribute.String("city.id", cityID.String()),
//		attribute.Int("limit", limit),
//	))
//	defer span.End()
//
//	l.logger.DebugContext(ctx, "Enhancing POI recommendations with semantic search",
//		slog.String("message", userMessage),
//		slog.String("city_id", cityID.String()))
//
//	if l.embeddingService == nil {
//		l.logger.WarnContext(ctx, "Embedding service not available, falling back to traditional search")
//		span.AddEvent("Embedding service not available")
//		return []locitypes.POIDetailedInfo{}, nil
//	}
//
//	// Generate embedding for user message combined with preferences
//	searchQuery := userMessage
//	if len(userPreferences) > 0 {
//		searchQuery += " " + strings.Join(userPreferences, " ")
//	}
//
//	queryEmbedding, err := l.embeddingService.GenerateQueryEmbedding(ctx, searchQuery)
//	if err != nil {
//		l.logger.ErrorContext(ctx, "Failed to generate query embedding",
//			slog.Any("error", err),
//			slog.String("query", searchQuery))
//		span.RecordError(err)
//		span.SetStatus(codes.Error, "Failed to generate query embedding")
//		return []locitypes.POIDetailedInfo{}, fmt.Errorf("failed to generate query embedding: %w", err)
//	}
//
//	// Search for similar POIs in the city
//	similarPOIs, err := l.poiRepo.FindSimilarPOIsByCity(ctx, queryEmbedding, cityID, limit)
//	if err != nil {
//		l.logger.ErrorContext(ctx, "Failed to find similar POIs", slog.Any("error", err))
//		span.RecordError(err)
//		span.SetStatus(codes.Error, "Failed to find similar POIs")
//		return []locitypes.POIDetailedInfo{}, fmt.Errorf("failed to find similar POIs: %w", err)
//	}
//
//	l.logger.InfoContext(ctx, "Found semantically similar POIs",
//		slog.Int("count", len(similarPOIs)),
//		slog.String("city_id", cityID.String()))
//	span.SetAttributes(
//		attribute.Int("similar_pois.count", len(similarPOIs)),
//		attribute.String("search.query", searchQuery),
//	)
//	span.SetStatus(codes.Ok, "Semantic POI recommendations enhanced")
//
//	return similarPOIs, nil
//}

// generateSemanticPOIRecommendations generates POI recommendations using semantic search

func (l *ServiceImpl) generateSemanticPOIRecommendations(ctx context.Context, userMessage string, cityID, userID uuid.UUID, userLocation *locitypes.UserLocation, semanticWeight float64) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "generateSemanticPOIRecommendations", trace.WithAttributes(
		attribute.String("user.message", userMessage),
		attribute.String("city.id", cityID.String()),
		attribute.String("user.id", userID.String()),
		attribute.Float64("semantic.weight", semanticWeight),
	))
	defer span.End()

	l.logger.DebugContext(ctx, "Generating semantic POI recommendations",
		slog.String("message", userMessage),
		slog.String("city_id", cityID.String()),
		slog.Float64("semantic_weight", semanticWeight))

	if l.embeddingService == nil {
		err := fmt.Errorf("embedding service not available")
		l.logger.ErrorContext(ctx, "Embedding service not available", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Embedding service not available")
		return nil, err
	}

	// Generate embedding for user message
	queryEmbedding, err := l.embeddingService.GenerateQueryEmbedding(ctx, userMessage)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to generate query embedding", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to generate query embedding")
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	var pois []locitypes.POIDetailedInfo

	// If user location is available, use hybrid search (spatial + semantic)
	if userLocation != nil && userLocation.UserLat != 0 && userLocation.UserLon != 0 {
		filter := locitypes.POIFilter{
			Location: locitypes.GeoPoint{
				Latitude:  userLocation.UserLat,
				Longitude: userLocation.UserLon,
			},
			Radius: userLocation.SearchRadiusKm,
		}

		hybridPOIs, err := l.poiRepo.SearchPOIsHybrid(ctx, filter, queryEmbedding, semanticWeight)
		if err != nil {
			l.logger.ErrorContext(ctx, "Failed to perform hybrid search", slog.Any("error", err))
			span.RecordError(err)
			// Fall back to semantic-only search
		} else {
			pois = hybridPOIs
			l.logger.InfoContext(ctx, "Used hybrid search for POI recommendations",
				slog.Int("poi_count", len(pois)))
			span.AddEvent("Used hybrid search")
		}
	}

	// If hybrid search failed or no location available, use semantic-only search
	if len(pois) == 0 {
		semanticPOIs, err := l.poiRepo.FindSimilarPOIsByCity(ctx, queryEmbedding, cityID, 10)
		if err != nil {
			l.logger.ErrorContext(ctx, "Failed to find similar POIs", slog.Any("error", err))
			span.RecordError(err)
			span.SetStatus(codes.Error, "Failed to find similar POIs")
			return nil, fmt.Errorf("failed to find similar POIs: %w", err)
		}
		pois = semanticPOIs
		l.logger.InfoContext(ctx, "Used semantic-only search for POI recommendations",
			slog.Int("poi_count", len(pois)))
		span.AddEvent("Used semantic-only search")
	}

	// Generate embeddings for new POIs if needed
	for i, p := range pois {
		if p.ID == uuid.Nil {
			continue
		}

		// Generate embedding for this POI if it doesn't have one
		embedding, err := l.embeddingService.GeneratePOIEmbedding(ctx, p.Name, p.DescriptionPOI, p.Category)
		if err != nil {
			l.logger.WarnContext(ctx, "Failed to generate embedding for POI",
				slog.Any("error", err),
				slog.String("poi_name", p.Name))
			continue
		}

		// Update POI with embedding
		err = l.poiRepo.UpdatePOIEmbedding(ctx, p.ID, embedding)
		if err != nil {
			l.logger.WarnContext(ctx, "Failed to update POI embedding",
				slog.Any("error", err),
				slog.String("poi_id", p.ID.String()))
		}

		pois[i] = p
	}

	l.logger.InfoContext(ctx, "Generated semantic POI recommendations",
		slog.String("message", userMessage),
		slog.Int("recommendations", len(pois)))
	span.SetAttributes(
		attribute.String("search.query", userMessage),
		attribute.Int("recommendations.count", len(pois)),
		attribute.Float64("semantic.weight", semanticWeight),
	)
	span.SetStatus(codes.Ok, "Semantic POI recommendations generated")

	return pois, nil
}

// handleSemanticRemovePOI handles removing POIs with semantic understanding

func (l *ServiceImpl) handleSemanticRemovePOI(ctx context.Context, message string, session *locitypes.ChatSession) string {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "handleSemanticRemovePOI")
	defer span.End()

	poiName := extractPOIName(message)
	if poiName == "" {
		return "I'd be happy to remove a POI from your itinerary! Could you please specify which place you'd like to remove?"
	}

	// Use semantic matching for removal - be more flexible with name matching
	for i, p := range session.CurrentItinerary.AIItineraryResponse.PointsOfInterest {
		// Check for exact match or semantic similarity
		if strings.EqualFold(p.Name, poiName) ||
			strings.Contains(strings.ToLower(p.Name), strings.ToLower(poiName)) ||
			strings.Contains(strings.ToLower(poiName), strings.ToLower(p.Name)) {

			removedName := p.Name
			session.CurrentItinerary.AIItineraryResponse.PointsOfInterest = append(
				session.CurrentItinerary.AIItineraryResponse.PointsOfInterest[:i],
				session.CurrentItinerary.AIItineraryResponse.PointsOfInterest[i+1:]...,
			)
			l.logger.InfoContext(ctx, "Removed POI from itinerary",
				slog.String("removed_poi", removedName))
			span.SetAttributes(attribute.String("removed_poi", removedName))
			return fmt.Sprintf("I've removed %s from your itinerary.", removedName)
		}
	}

	return fmt.Sprintf("I couldn't find %s in your itinerary. Here's what you currently have: %s",
		poiName, strings.Join(func() []string {
			var names []string
			for _, p := range session.CurrentItinerary.AIItineraryResponse.PointsOfInterest {
				names = append(names, p.Name)
			}
			return names
		}(), ", "))
}

// min helper function
