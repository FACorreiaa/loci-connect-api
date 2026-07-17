package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func (l *ServiceImpl) CollectResults(resultCh <-chan locitypes.GenAIResponse) (itinerary locitypes.AiCityResponse, llmInteractionID uuid.UUID, rawPersonalisedPOIs []locitypes.POIDetailedInfo, errors []error) {
	for res := range resultCh {
		if res.Err != nil {
			errors = append(errors, res.Err)
			continue
		}
		if res.City != "" {
			itinerary.GeneralCityData.City = res.City
			itinerary.GeneralCityData.Country = res.Country
			itinerary.GeneralCityData.Description = res.CityDescription
			itinerary.GeneralCityData.StateProvince = res.StateProvince
			itinerary.GeneralCityData.CenterLatitude = res.Latitude
			itinerary.GeneralCityData.CenterLongitude = res.Longitude
		}
		if res.ItineraryName != "" {
			itinerary.AIItineraryResponse.ItineraryName = res.ItineraryName
			itinerary.AIItineraryResponse.OverallDescription = res.ItineraryDescription
		}
		if len(res.GeneralPOI) > 0 {
			itinerary.PointsOfInterest = res.GeneralPOI
		}
		if len(res.PersonalisedPOI) > 0 {
			itinerary.AIItineraryResponse.PointsOfInterest = res.PersonalisedPOI
			rawPersonalisedPOIs = res.PersonalisedPOI
			llmInteractionID = res.LlmInteractionID
		}
	}
	return itinerary, llmInteractionID, rawPersonalisedPOIs, errors
}

func (l *ServiceImpl) HandleCityData(ctx context.Context, cityData locitypes.GeneralCityData) (cityID uuid.UUID, err error) {
	c, err := l.cityRepo.FindCityByNameAndCountry(ctx, cityData.City, cityData.Country)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to check city existence: %w", err)
	}
	if c == nil {
		cityDetail := locitypes.CityDetail{
			Name:            cityData.City,
			Country:         cityData.Country,
			StateProvince:   cityData.StateProvince,
			AiSummary:       cityData.Description,
			CenterLatitude:  &cityData.CenterLatitude,
			CenterLongitude: &cityData.CenterLongitude,
		}
		cityID, err = l.cityRepo.SaveCity(ctx, cityDetail)
		if err != nil {
			return uuid.Nil, fmt.Errorf("failed to save city: %w", err)
		}
	} else {
		cityID = c.ID
	}
	return cityID, nil
}

func (l *ServiceImpl) HandleGeneralPOIs(ctx context.Context, pois []locitypes.POIDetailedInfo, cityID uuid.UUID) {
	for _, p := range pois {
		existingPoi, err := l.poiRepo.FindPoiByNameAndCity(ctx, p.Name, cityID)
		if err != nil {
			l.logger.WarnContext(ctx, "Failed to check POI existence", slog.String("poi_name", p.Name), slog.Any("error", err))
			continue
		}
		if existingPoi == nil {
			_, err = l.poiRepo.SavePoi(ctx, p, cityID)
			if err != nil {
				l.logger.WarnContext(ctx, "Failed to save POI", slog.String("poi_name", p.Name), slog.Any("error", err))
			}
		}
	}
}

func (l *ServiceImpl) HandlePersonalisedPOIs(ctx context.Context, pois []locitypes.POIDetailedInfo, cityID uuid.UUID, userLocation *locitypes.UserLocation, llmInteractionID, userID, profileID uuid.UUID) ([]locitypes.POIDetailedInfo, error) {
	if userLocation == nil || len(pois) == 0 {
		return pois, nil // No sorting possible
	}

	// Check if cityID is valid, if not, skip itinerary creation to avoid foreign key constraint errors
	if cityID == uuid.Nil || cityID.String() == "00000000-0000-0000-0000-000000000000" {
		l.logger.WarnContext(ctx, "Skipping itinerary creation due to invalid cityID",
			slog.String("cityID", cityID.String()))
		return pois, nil // Return POIs without sorting/saving to avoid database errors
	}

	err := l.llmInteractionRepo.SaveLlmSuggestedPOIsBatch(ctx, pois, userID, profileID, llmInteractionID, cityID)
	if err != nil {
		return nil, fmt.Errorf("failed to save personalised POIs: %w", err)
	}

	itineraryID, err := l.poiRepo.SaveItinerary(ctx, userID, cityID)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to save itinerary, skipping itinerary creation",
			slog.Any("error", err),
			slog.String("cityID", cityID.String()),
			slog.String("userID", userID.String()))
		// Don't return error, just skip itinerary creation and continue with POI processing
		return pois, nil
	}

	if err := l.poiRepo.SaveItineraryPOIs(ctx, itineraryID, pois); err != nil {
		return nil, fmt.Errorf("failed to save itinerary POIs: %w", err)
	}

	sortedPois, err := l.llmInteractionRepo.GetLlmSuggestedPOIsByInteractionSortedByDistance(ctx, llmInteractionID, cityID, *userLocation)
	if err != nil {
		l.logger.ErrorContext(ctx, "Failed to fetch sorted POIs", slog.Any("error", err))
		return pois, nil // Return unsorted POIs
	}
	return sortedPois, nil
}

// GenerateEnhancedPersonalisedPOIWorker generates personalized POIs with domain-aware filtering
func (l *ServiceImpl) GenerateEnhancedPersonalisedPOIWorker(ctx context.Context,
	cityName string, userID, profileID uuid.UUID, resultCh chan<- locitypes.GenAIResponse,
	enhancedPromptData string, domain locitypes.DomainType, config *genai.GenerateContentConfig,
) {
	ctx, span := otel.Tracer("LlmInteractionService").Start(ctx, "GenerateEnhancedPersonalisedPOIWorker", trace.WithAttributes(
		attribute.String("city.name", cityName),
		attribute.String("user.id", userID.String()),
		attribute.String("profile.id", profileID.String()),
		attribute.String("domain", string(domain)),
	))
	defer span.End()

	startTime := time.Now()

	// Create enhanced prompt based on domain
	prompt := l.getEnhancedPersonalizedPOIPrompt(cityName, enhancedPromptData, domain)
	span.SetAttributes(attribute.Int("prompt.length", len(prompt)))

	response, err := l.aiClient.Generate(ctx, prompt, config)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "AI generation failed")
		resultCh <- locitypes.GenAIResponse{Err: fmt.Errorf("failed to generate enhanced personalized POIs: %w", err)}
		return
	}

	duration := time.Since(startTime)
	span.SetAttributes(attribute.Int64("generation.duration_ms", duration.Milliseconds()))

	var txt string
	for _, candidate := range response.Candidates {
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			txt = candidate.Content.Parts[0].Text
			break
		}
	}
	if txt == "" {
		err := fmt.Errorf("no valid enhanced personalized POI content from AI")
		span.RecordError(err)
		span.SetStatus(codes.Error, "Empty response from AI")
		resultCh <- locitypes.GenAIResponse{Err: err}
		return
	}
	span.SetAttributes(attribute.Int("response.length", len(txt)))

	cleanTxt := generativeAI.CleanJSON(txt)
	var itineraryData locitypes.AIItineraryResponse
	if err := json.Unmarshal([]byte(cleanTxt), &itineraryData); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to parse enhanced personalized POI JSON")
		resultCh <- locitypes.GenAIResponse{Err: fmt.Errorf("failed to parse enhanced personalized POI JSON: %w", err)}
		return
	}

	span.SetAttributes(attribute.Int("pois.count", len(itineraryData.PointsOfInterest)))
	span.SetStatus(codes.Ok, "Enhanced personalized POIs generated successfully")
	resultCh <- locitypes.GenAIResponse{
		ItineraryName:        itineraryData.ItineraryName,
		ItineraryDescription: itineraryData.OverallDescription,
		PersonalisedPOI:      itineraryData.PointsOfInterest,
		LlmInteractionID:     uuid.New(), // Generate a new LLM interaction ID
	}
}

func (l *ServiceImpl) ensureItineraryExists(session *locitypes.ChatSession) {
	if session.CurrentItinerary == nil {
		session.CurrentItinerary = &locitypes.AiCityResponse{
			AIItineraryResponse: locitypes.AIItineraryResponse{
				ItineraryName:      fmt.Sprintf("Trip to %s", session.SessionContext.CityName),
				OverallDescription: fmt.Sprintf("Exploring %s", session.SessionContext.CityName),
				PointsOfInterest:   []locitypes.POIDetailedInfo{},
			},
		}
	}
	if session.CurrentItinerary.AIItineraryResponse.PointsOfInterest == nil {
		session.CurrentItinerary.AIItineraryResponse.PointsOfInterest = []locitypes.POIDetailedInfo{}
	}
}

// parseCityDataFromResponse extracts and parses city data from streamed response content

func (l *ServiceImpl) parseCityDataFromResponse(_ context.Context, responseContent string) (*locitypes.GeneralCityData, error) {
	// Clean the response by extracting JSON content between ```json and ```
	cleanedResponse := responseContent

	// Look for JSON blocks in the response
	if strings.Contains(responseContent, "```json") {
		start := strings.Index(responseContent, "```json")
		if start != -1 {
			start += len("```json")
			end := strings.Index(responseContent[start:], "```")
			if end != -1 {
				cleanedResponse = strings.TrimSpace(responseContent[start : start+end])
			}
		}
	}

	// Validate JSON
	if !json.Valid([]byte(cleanedResponse)) {
		return nil, fmt.Errorf("invalid JSON in city data response")
	}

	// Try to parse as GeneralCityData
	var generalCity locitypes.GeneralCityData
	if err := json.Unmarshal([]byte(cleanedResponse), &generalCity); err != nil {
		return nil, fmt.Errorf("failed to unmarshal city data: %w", err)
	}

	// Validate that we have minimum required city data
	if generalCity.City == "" {
		return nil, fmt.Errorf("parsed city data is missing city name")
	}

	return &generalCity, nil
}

// streamWorkerWithResponseAndCache handles streaming for a single worker with response capture and cache support.
