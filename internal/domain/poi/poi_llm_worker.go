package poi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type nearbyPromptBuilder func(lat, lon, distance float64, strict bool) string

func (s *ServiceImpl) getNearbyByDomain(
	ctx context.Context,
	userID uuid.UUID,
	lat, lon, distance float64,
	strict bool,
	spanName string,
	promptFn nearbyPromptBuilder,
	resultCh chan<- locitypes.GenAIResponse,
	config *genai.GenerateContentConfig,
) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, spanName, trace.WithAttributes(
		attribute.Float64("latitude", lat),
		attribute.Float64("longitude", lon),
		attribute.Float64("distance.m", distance),
		attribute.String("user.id", userID.String()),
		attribute.Bool("prompt.strict", strict),
	))
	defer span.End()

	prompt := promptFn(lat, lon, distance, strict)
	span.SetAttributes(attribute.Int("prompt.length", len(prompt)))

	if s.aiClient == nil {
		err := fmt.Errorf("AI client is not available - check API key configuration")
		span.RecordError(err)
		span.SetStatus(codes.Error, "AI client unavailable")
		resultCh <- locitypes.GenAIResponse{Err: err}
		return
	}

	startTime := time.Now()
	response, err := s.generateWithLLMSlot(ctx, prompt, config)
	span.SetAttributes(attribute.Int("response.latency_ms", int(time.Since(startTime).Milliseconds())))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to generate nearby POIs")
		resultCh <- locitypes.GenAIResponse{Err: fmt.Errorf("failed to generate nearby POIs: %w", err)}
		return
	}

	txt := firstCandidateText(response)
	if txt == "" {
		err := fmt.Errorf("no valid nearby POI content from AI")
		span.RecordError(err)
		span.SetStatus(codes.Error, "Empty response from AI")
		resultCh <- locitypes.GenAIResponse{Err: err}
		return
	}
	span.SetAttributes(attribute.Int("response.length", len(txt)))

	cleanTxt := cleanJSONResponse(txt)
	var poiData struct {
		PointsOfInterest []locitypes.POIDetailedInfo `json:"points_of_interest"`
	}
	if err := json.Unmarshal([]byte(cleanTxt), &poiData); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to parse nearby POI JSON")
		resultCh <- locitypes.GenAIResponse{Err: fmt.Errorf("failed to parse nearby POI JSON: %w", err)}
		return
	}

	s.logger.DebugContext(ctx, "generated nearby POI JSON response", slog.String("response", cleanTxt))

	span.SetAttributes(attribute.Int("pois.count", len(poiData.PointsOfInterest)))
	span.SetStatus(codes.Ok, "Nearby POIs generated successfully")
	resultCh <- locitypes.GenAIResponse{
		GeneralPOI: poiData.PointsOfInterest,
		ModelName:  s.aiClient.Model(),
		Prompt:     prompt,
		Response:   cleanTxt,
	}
}

func firstCandidateText(response *genai.GenerateContentResponse) string {
	if response == nil {
		return ""
	}
	for _, candidate := range response.Candidates {
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			return candidate.Content.Parts[0].Text
		}
	}
	return ""
}

func (s *ServiceImpl) getGeneralPOIByDistance(
	ctx context.Context,
	userID uuid.UUID,
	lat, lon, distance float64,
	strict bool,
	resultCh chan<- locitypes.GenAIResponse,
	config *genai.GenerateContentConfig,
) {
	s.getNearbyByDomain(ctx, userID, lat, lon, distance, strict, "GenerateGeneralPOIWorker", getGeneralPOIByDistancePrompt, resultCh, config)
}

func (s *ServiceImpl) getGeneralRestaurantByDistance(
	ctx context.Context,
	userID uuid.UUID,
	lat, lon, distance float64,
	resultCh chan<- locitypes.GenAIResponse,
	config *genai.GenerateContentConfig,
) {
	s.getNearbyByDomain(ctx, userID, lat, lon, distance, false, "GenerateRestaurantWorker", wrapNearbyPrompt(getRestaurantsNearbyPrompt), resultCh, config)
}

func (s *ServiceImpl) getGeneralActivitiesByDistance(
	ctx context.Context,
	userID uuid.UUID,
	lat, lon, distance float64,
	resultCh chan<- locitypes.GenAIResponse,
	config *genai.GenerateContentConfig,
) {
	s.getNearbyByDomain(ctx, userID, lat, lon, distance, false, "GenerateActivitiesWorker", wrapNearbyPrompt(getActivitiesNearbyPrompt), resultCh, config)
}

func (s *ServiceImpl) getGeneralHotelsByDistance(
	ctx context.Context,
	userID uuid.UUID,
	lat, lon, distance float64,
	resultCh chan<- locitypes.GenAIResponse,
	config *genai.GenerateContentConfig,
) {
	s.getNearbyByDomain(ctx, userID, lat, lon, distance, false, "GenerateHotelsWorker", wrapNearbyPrompt(getHotelsNeabyPrompt), resultCh, config)
}

func (s *ServiceImpl) getGeneralAttractionsByDistance(
	ctx context.Context,
	userID uuid.UUID,
	lat, lon, distance float64,
	resultCh chan<- locitypes.GenAIResponse,
	config *genai.GenerateContentConfig,
) {
	s.getNearbyByDomain(ctx, userID, lat, lon, distance, false, "GenerateAttractionsWorker", wrapNearbyPrompt(getAttractionsNeabyPrompt), resultCh, config)
}

func wrapNearbyPrompt(fn func(lat, lon, distance float64) string) nearbyPromptBuilder {
	return func(lat, lon, distance float64, _ bool) string {
		return fn(lat, lon, distance)
	}
}
