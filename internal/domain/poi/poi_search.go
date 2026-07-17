package poi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (s *ServiceImpl) SearchPOIs(ctx context.Context, filter locitypes.POIFilter) ([]locitypes.POIDetailedInfo, error) {
	pois, err := s.poiRepository.SearchPOIs(ctx, filter)
	if err != nil {
		s.logger.Error("failed to search POIs", "error", err)
		return nil, err
	}
	return pois, nil
}

// SearchPOIsSemantic performs semantic search for POIs using natural language queries
func (s *ServiceImpl) SearchPOIsSemantic(ctx context.Context, query string, limit int) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "SearchPOIsSemantic", trace.WithAttributes(
		attribute.String("query", query),
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "SearchPOIsSemantic"))

	if s.embeddingService == nil {
		err := fmt.Errorf("embedding service not available")
		l.ErrorContext(ctx, "Embedding service not initialized", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Embedding service not available")
		return nil, err
	}

	// Generate embedding for the query
	queryEmbedding, err := s.embedQueryMetered(ctx, query)
	if err != nil {
		l.ErrorContext(ctx, "Failed to generate query embedding",
			slog.Any("error", err),
			slog.String("query", query))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to generate query embedding")
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search for similar POIs
	pois, err := s.poiRepository.FindSimilarPOIs(ctx, queryEmbedding, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to find similar POIs", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to find similar POIs")
		return nil, fmt.Errorf("failed to find similar POIs: %w", err)
	}

	l.InfoContext(ctx, "Semantic search completed",
		slog.String("query", query),
		slog.Int("results", len(pois)))
	span.SetAttributes(
		attribute.String("query", query),
		attribute.Int("results.count", len(pois)),
	)
	span.SetStatus(codes.Ok, "Semantic search completed")

	return pois, nil
}

// SearchPOIsSemanticByCity performs semantic search for POIs within a specific city
func (s *ServiceImpl) SearchPOIsSemanticByCity(ctx context.Context, query string, cityID uuid.UUID, limit int) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "SearchPOIsSemanticByCity", trace.WithAttributes(
		attribute.String("query", query),
		attribute.String("city.id", cityID.String()),
		attribute.Int("limit", limit),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "SearchPOIsSemanticByCity"))

	if s.embeddingService == nil {
		err := fmt.Errorf("embedding service not available")
		l.ErrorContext(ctx, "Embedding service not initialized", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Embedding service not available")
		return nil, err
	}

	// Generate embedding for the query
	queryEmbedding, err := s.embedQueryMetered(ctx, query)
	if err != nil {
		l.ErrorContext(ctx, "Failed to generate query embedding",
			slog.Any("error", err),
			slog.String("query", query))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to generate query embedding")
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search for similar POIs in the specified city
	pois, err := s.poiRepository.FindSimilarPOIsByCity(ctx, queryEmbedding, cityID, limit)
	if err != nil {
		l.ErrorContext(ctx, "Failed to find similar POIs by city", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to find similar POIs by city")
		return nil, fmt.Errorf("failed to find similar POIs by city: %w", err)
	}

	l.InfoContext(ctx, "Semantic search by city completed",
		slog.String("query", query),
		slog.String("city_id", cityID.String()),
		slog.Int("results", len(pois)))
	span.SetAttributes(
		attribute.String("query", query),
		attribute.String("city.id", cityID.String()),
		attribute.Int("results.count", len(pois)),
	)
	span.SetStatus(codes.Ok, "Semantic search by city completed")

	return pois, nil
}

// SearchPOIsByQueryAndCity performs a semantic search for POIs using a query string and city name
// If no results are found in the database, it falls back to LLM generation
func (s *ServiceImpl) SearchPOIsByQueryAndCity(ctx context.Context, query, cityName string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "SearchPOIsByQueryAndCity", trace.WithAttributes(
		attribute.String("query", query),
		attribute.String("city.name", cityName),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "SearchPOIsByQueryAndCity"))
	l.DebugContext(ctx, "Searching POIs by query and city name",
		slog.String("query", query),
		slog.String("city", cityName))

	var pois []locitypes.POIDetailedInfo
	var cityFound bool

	// Try exact case-insensitive match first (doesn't require pg_trgm extension)
	city, err := s.cityRepo.FindCityByNameAndCountry(ctx, cityName, "")
	if err != nil {
		l.WarnContext(ctx, "Failed to find city by name",
			slog.Any("error", err),
			slog.String("city_name", cityName))
	} else if city != nil {
		cityFound = true
		l.InfoContext(ctx, "City found in database",
			slog.String("city_name", city.Name),
			slog.String("city_id", city.ID.String()))
		span.SetAttributes(
			attribute.String("city.name", city.Name),
			attribute.String("city.id", city.ID.String()),
		)

		// Use the semantic search by city method
		limit := 20 // default limit
		pois, err = s.SearchPOIsSemanticByCity(ctx, query, city.ID, limit)
		if err != nil {
			l.WarnContext(ctx, "Database search failed, will try LLM fallback",
				slog.Any("error", err),
				slog.String("query", query),
				slog.String("city_id", city.ID.String()))
		}
	}

	// If we didn't find the city or got no POI results, fall back to LLM generation
	if !cityFound || len(pois) == 0 {
		l.InfoContext(ctx, "Falling back to LLM generation",
			slog.String("query", query),
			slog.String("city", cityName),
			slog.Bool("city_found", cityFound),
			slog.Int("db_results", len(pois)))

		span.AddEvent("fallback_to_llm")

		// Generate POIs using LLM
		llmPOIs, err := s.generatePOIsWithLLM(ctx, query, cityName)
		if err != nil {
			l.ErrorContext(ctx, "Failed to generate POIs with LLM",
				slog.Any("error", err),
				slog.String("query", query),
				slog.String("city", cityName))
			span.RecordError(err)
			span.SetStatus(codes.Error, "Failed to generate POIs with LLM")
			return nil, fmt.Errorf("failed to generate POIs: %w", err)
		}

		l.InfoContext(ctx, "Successfully generated POIs with LLM",
			slog.String("query", query),
			slog.String("city", cityName),
			slog.Int("results", len(llmPOIs)))
		span.SetAttributes(
			attribute.Int("llm_results.count", len(llmPOIs)),
			attribute.String("source", "llm"),
		)

		// Track search (async, don't fail on error)
		if s.discoverRepo != nil {
			go func(ctx context.Context) {
				if err := s.discoverRepo.TrackSearch(ctx, uuid.Nil, query, cityName, "llm", len(llmPOIs)); err != nil {
					s.logger.WarnContext(ctx, "failed to track LLM search", slog.Any("error", err))
				}
			}(context.Background())
		}

		return llmPOIs, nil
	}

	l.InfoContext(ctx, "Successfully searched POIs from database",
		slog.String("query", query),
		slog.String("city", cityName),
		slog.Int("results", len(pois)))
	span.SetAttributes(
		attribute.Int("results.count", len(pois)),
		attribute.String("source", "database"),
	)
	span.SetStatus(codes.Ok, "Search completed successfully")

	// Track search (async, don't fail on error)
	if s.discoverRepo != nil {
		go func(ctx context.Context) {
			if err := s.discoverRepo.TrackSearch(ctx, uuid.Nil, query, cityName, "database", len(pois)); err != nil {
				s.logger.WarnContext(ctx, "failed to track database search", slog.Any("error", err))
			}
		}(context.Background())
	}

	return pois, nil
}

// generatePOIsWithLLM generates POIs using LLM when database search returns no results
func (s *ServiceImpl) generatePOIsWithLLM(ctx context.Context, query, cityName string) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "generatePOIsWithLLM", trace.WithAttributes(
		attribute.String("query", query),
		attribute.String("city", cityName),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "generatePOIsWithLLM"))

	if s.aiClient == nil {
		err := fmt.Errorf("AI client is not available - check API key configuration")
		l.ErrorContext(ctx, "AI client not initialized")
		span.RecordError(err)
		span.SetStatus(codes.Error, "AI client not available")
		return nil, err
	}

	// Create discover search prompt
	prompt := fmt.Sprintf(`You are a travel discovery assistant. Generate a list of POIs (Points of Interest) based on the user's search query.

Search Query: "%s"
Location: %s

Please return a JSON response with an array of results. Each result should include:
- name: The name of the place
- latitude: Latitude coordinate
- longitude: Longitude coordinate
- category: Category (e.g., "restaurant", "hotel", "attraction", "activity")
- description: A brief description
- address: Full address
- rating: Rating from 0-5
- price_level: Price level ("$", "$$", "$$$", "$$$$")

Return ONLY the JSON, no markdown code blocks, in this format:
{
  "results": [
    {
      "name": "...",
      "latitude": 0.0,
      "longitude": 0.0,
      "category": "...",
      "description": "...",
      "address": "...",
      "rating": 4.5,
      "price_level": "$$"
    }
  ]
}

Generate 5-10 relevant results.`, query, cityName)

	l.DebugContext(ctx, "Calling LLM for discover search",
		slog.String("query", query),
		slog.String("city", cityName))

	startTime := time.Now()
	response, err := s.generateWithLLMSlot(ctx, prompt, &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.7),
	})
	latencyMs := time.Since(startTime).Milliseconds()

	if err != nil {
		l.ErrorContext(ctx, "LLM request failed", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "LLM request failed")
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	if response == nil || len(response.Candidates) == 0 {
		err := fmt.Errorf("empty LLM response")
		l.ErrorContext(ctx, "Empty LLM response")
		span.RecordError(err)
		span.SetStatus(codes.Error, "Empty LLM response")
		return nil, err
	}

	// Extract text from response
	var txt string
	for _, candidate := range response.Candidates {
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			txt = candidate.Content.Parts[0].Text
			break
		}
	}

	// Clean the LLM response (remove markdown, trim, fix JSON issues)
	responseStr := cleanLLMResponse(txt)

	l.DebugContext(ctx, "LLM response received",
		slog.Int64("latency_ms", latencyMs),
		slog.Int("response_length", len(responseStr)),
		slog.String("response_preview", responseStr[:minInt(200, len(responseStr))]))

	// Parse JSON response
	var searchResponse struct {
		Results []struct {
			Name        string  `json:"name"`
			Latitude    float64 `json:"latitude"`
			Longitude   float64 `json:"longitude"`
			Category    string  `json:"category"`
			Description string  `json:"description"`
			Address     string  `json:"address"`
			Rating      float64 `json:"rating"`
			PriceLevel  string  `json:"price_level"`
			Website     *string `json:"website,omitempty"`
			PhoneNumber *string `json:"phone_number,omitempty"`
		} `json:"results"`
	}

	if err := json.Unmarshal([]byte(responseStr), &searchResponse); err != nil {
		// Log the full response for debugging
		responsePreview := responseStr
		if len(responseStr) > 1000 {
			responsePreview = responseStr[:1000] + "... (truncated)"
		}
		l.ErrorContext(ctx, "Failed to parse LLM JSON response",
			slog.Any("error", err),
			slog.String("response", responsePreview),
			slog.Int("response_length", len(responseStr)))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to parse LLM response")
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	// Convert to POIDetailedInfo
	pois := make([]locitypes.POIDetailedInfo, len(searchResponse.Results))
	for i, result := range searchResponse.Results {
		poiID := uuid.New()
		pois[i] = locitypes.POIDetailedInfo{
			ID:             poiID,
			Name:           result.Name,
			Latitude:       result.Latitude,
			Longitude:      result.Longitude,
			Category:       result.Category,
			DescriptionPOI: result.Description,
			Description:    result.Description,
			Address:        result.Address,
			Rating:         result.Rating,
			PriceLevel:     result.PriceLevel,
			City:           cityName,
			Source:         "llm",
			CreatedAt:      time.Now(),
		}

		if result.Website != nil {
			pois[i].Website = *result.Website
		}
		if result.PhoneNumber != nil {
			pois[i].PhoneNumber = *result.PhoneNumber
		}
	}

	l.InfoContext(ctx, "Successfully generated POIs from LLM",
		slog.String("query", query),
		slog.String("city", cityName),
		slog.Int("count", len(pois)))
	span.SetAttributes(attribute.Int("pois.generated", len(pois)))
	span.SetStatus(codes.Ok, "POIs generated successfully")

	return pois, nil
}

// SearchPOIsHybrid performs hybrid search combining spatial and semantic similarity
func (s *ServiceImpl) SearchPOIsHybrid(ctx context.Context, filter locitypes.POIFilter, query string, semanticWeight float64) ([]locitypes.POIDetailedInfo, error) {
	ctx, span := otel.Tracer("POIService").Start(ctx, "SearchPOIsHybrid", trace.WithAttributes(
		attribute.String("query", query),
		attribute.Float64("semantic.weight", semanticWeight),
		attribute.Float64("location.latitude", filter.Location.Latitude),
		attribute.Float64("location.longitude", filter.Location.Longitude),
		attribute.Float64("radius", filter.Radius),
	))
	defer span.End()

	l := s.logger.With(slog.String("method", "SearchPOIsHybrid"))

	if s.embeddingService == nil {
		err := fmt.Errorf("embedding service not available")
		l.ErrorContext(ctx, "Embedding service not initialized", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Embedding service not available")
		return nil, err
	}

	// Validate semantic weight
	if semanticWeight < 0 || semanticWeight > 1 {
		err := fmt.Errorf("semantic weight must be between 0 and 1, got: %f", semanticWeight)
		l.ErrorContext(ctx, "Invalid semantic weight", slog.Float64("semantic_weight", semanticWeight))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Invalid semantic weight")
		return nil, err
	}

	// Generate embedding for the query
	queryEmbedding, err := s.embedQueryMetered(ctx, query)
	if err != nil {
		l.ErrorContext(ctx, "Failed to generate query embedding",
			slog.Any("error", err),
			slog.String("query", query))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to generate query embedding")
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Perform hybrid search
	pois, err := s.poiRepository.SearchPOIsHybrid(ctx, filter, queryEmbedding, semanticWeight)
	if err != nil {
		l.ErrorContext(ctx, "Failed to perform hybrid search", slog.Any("error", err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to perform hybrid search")
		return nil, fmt.Errorf("failed to perform hybrid search: %w", err)
	}

	l.InfoContext(ctx, "Hybrid search completed",
		slog.String("query", query),
		slog.Float64("semantic_weight", semanticWeight),
		slog.Int("results", len(pois)))
	span.SetAttributes(
		attribute.String("query", query),
		attribute.Float64("semantic.weight", semanticWeight),
		attribute.Int("results.count", len(pois)),
	)
	span.SetStatus(codes.Ok, "Hybrid search completed")

	return pois, nil
}
