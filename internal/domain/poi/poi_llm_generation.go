package poi

import (
	"context"
	"log/slog"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
)

// maxLLMPOIAttempts bounds how many times we ask the LLM for nearby POIs.
// The first attempt uses the standard prompt; subsequent attempts use a
// stricter prompt that forbids empty/null output.
const maxLLMPOIAttempts = 2

// generateAndEnrichPOIs generates POIs via the LLM with a re-prompt loop.
//
// The original bug: a model reply of bare "null" unmarshals into a nil slice
// with no error, so an empty result was silently returned as success. Here we
// detect an empty result *after* enrichment (which also catches the case where
// every generated POI is filtered out by the search radius) and retry once
// with a stricter prompt. If the model still produces nothing usable we return
// an empty slice and a nil error, so the caller surfaces an honest "no
// results" rather than a masked success.
func (s *ServiceImpl) generateAndEnrichPOIs(ctx context.Context, userID uuid.UUID, lat, lon, distance float64) ([]locitypes.POIDetailedInfo, error) {
	for attempt := 1; attempt <= maxLLMPOIAttempts; attempt++ {
		strict := attempt > 1

		genAIResponse, err := s.generatePOIsFromLLM(ctx, userID, lat, lon, distance, strict)
		if err != nil {
			// Transient errors are already retried inside the SDK; anything
			// surfacing here is non-transient (bad output, auth, etc.).
			return nil, err
		}

		enrichedPOIs := s.enrichAndFilterLLMResponse(genAIResponse.GeneralPOI, lat, lon, distance)
		for i := range enrichedPOIs {
			enrichedPOIs[i].Source = "llm_suggested_pois"
		}

		if len(enrichedPOIs) == 0 {
			s.logger.WarnContext(ctx, "LLM produced no usable POIs",
				slog.Int("attempt", attempt),
				slog.Int("max_attempts", maxLLMPOIAttempts),
				slog.Int("raw_count", len(genAIResponse.GeneralPOI)),
				slog.Float64("distance_m", distance),
				slog.Bool("retry_strict", attempt < maxLLMPOIAttempts))
			continue
		}

		s.persistLLMPOIs(ctx, userID, lat, lon, distance, enrichedPOIs, genAIResponse)
		return enrichedPOIs, nil
	}

	s.logger.InfoContext(ctx, "LLM produced no POIs after retries; returning empty result",
		slog.Float64("lat", lat), slog.Float64("lon", lon), slog.Float64("distance_m", distance))
	return []locitypes.POIDetailedInfo{}, nil
}

// enrichLLMWithRetry runs an LLM POI generation closure, enriches/filters the
// result by radius, and retries once if the model yields nothing usable.
// Returns an empty slice with a nil error if still empty, so callers surface an
// honest "no results" instead of the old silent "null = success". Caching and
// any persistence stay with the caller (domain cache keys differ).
func (s *ServiceImpl) enrichLLMWithRetry(
	ctx context.Context,
	lat, lon, distance float64,
	domain string,
	gen func() (*locitypes.GenAIResponse, error),
) ([]locitypes.POIDetailedInfo, error) {
	for attempt := 1; attempt <= maxLLMPOIAttempts; attempt++ {
		genAIResponse, err := gen()
		if err != nil {
			return nil, err
		}

		enriched := s.enrichAndFilterLLMResponse(genAIResponse.GeneralPOI, lat, lon, distance)
		for i := range enriched {
			enriched[i].Source = "llm_suggested_pois"
		}
		if len(enriched) > 0 {
			return enriched, nil
		}

		s.logger.WarnContext(ctx, "LLM produced no usable POIs",
			slog.String("domain", domain),
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", maxLLMPOIAttempts),
			slog.Int("raw_count", len(genAIResponse.GeneralPOI)),
			slog.Float64("distance_m", distance))
	}

	s.logger.InfoContext(ctx, "LLM produced no POIs after retries; returning empty result",
		slog.String("domain", domain), slog.Float64("distance_m", distance))
	return []locitypes.POIDetailedInfo{}, nil
}

// persistLLMPOIs records the LLM interaction and saves the generated POIs.
// Persistence failures are logged but non-fatal: the POIs are still returned to
// the caller so a transient DB hiccup doesn't blank out a good result.
func (s *ServiceImpl) persistLLMPOIs(ctx context.Context, userID uuid.UUID, lat, lon, distance float64, enrichedPOIs []locitypes.POIDetailedInfo, genAIResponse *locitypes.GenAIResponse) {
	interaction := &locitypes.LlmInteraction{
		UserID:    userID,
		ModelName: genAIResponse.ModelName,
		Prompt:    genAIResponse.Prompt,
		Response:  genAIResponse.Response,
		Latitude:  &lat,
		Longitude: &lon,
		Distance:  &distance,
	}

	llmInteractionID, err := s.poiRepository.SaveLlmInteraction(ctx, interaction)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to save LLM interaction", slog.Any("error", err))
		return
	}

	// Synchronous save to ensure POIs are available immediately.
	if err := s.poiRepository.SaveLlmPoisToDatabase(ctx, userID, enrichedPOIs, genAIResponse, llmInteractionID); err != nil {
		s.logger.WarnContext(ctx, "Failed to save LLM POIs to database", slog.Any("error", err))
	}
}
