package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// nestedKeyForPart names the envelope field a part's JSON hides its payload
// in, or "" when the payload is the whole document.
func nestedKeyForPart(p generationPart) string {
	switch p {
	case partGeneralPOIs:
		return "points_of_interest"
	case partHotels:
		return "hotels"
	case partRestaurants:
		return "restaurants"
	case partActivities:
		return "activities"
	}
	return ""
}

// parseGeneratedPart decodes one generated part into target.
//
// This is the single parser for generated output: the read path uses it to
// build the response and the write path uses it (through
// validateGeneratedPart) to decide whether the output is worth storing. One
// implementation means a cached answer is always one the renderer could read.
func parseGeneratedPart(part generationPart, raw string, target any) error {
	clean := extractJSONFromMarkdown(raw)
	nested := nestedKeyForPart(part)
	if nested == "" {
		if err := json.Unmarshal([]byte(clean), target); err != nil {
			return fmt.Errorf("parse %s: %w", part, err)
		}
		return nil
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(clean), &envelope); err != nil {
		return fmt.Errorf("parse %s envelope: %w", part, err)
	}
	payload, ok := envelope[nested]
	if !ok {
		return fmt.Errorf("parse %s: no %q field", part, nested)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("parse %s.%s: %w", part, nested, err)
	}
	return nil
}

// validateGeneratedPart reports whether a generated part is worth keeping.
//
// Unparseable or empty output used to be cached like any other non-empty
// stream, then replayed and re-persisted for the rest of its lifetime. A
// durable layer makes that a much longer mistake, so nothing is written until
// it has been read back as the structure it claims to be.
func validateGeneratedPart(part generationPart, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	switch part {
	case partCityData:
		var city locitypes.GeneralCityData
		return parseGeneratedPart(part, raw, &city) == nil && city.City != ""
	case partItinerary:
		var itinerary locitypes.AIItineraryResponse
		if parseGeneratedPart(part, raw, &itinerary) != nil {
			return false
		}
		return itinerary.ItineraryName != "" || len(itinerary.PointsOfInterest) > 0
	case partGeneralPOIs, partActivities:
		var pois []locitypes.POIDetailedInfo
		return parseGeneratedPart(part, raw, &pois) == nil && len(pois) > 0
	case partHotels:
		var hotels []locitypes.HotelDetailedInfo
		return parseGeneratedPart(part, raw, &hotels) == nil && len(hotels) > 0
	case partRestaurants:
		var restaurants []locitypes.RestaurantDetailedInfo
		return parseGeneratedPart(part, raw, &restaurants) == nil && len(restaurants) > 0
	}
	return false
}

// persistGenerations stores what the provider produced this turn and reports
// every part to PostHog.
//
// Only parts the provider actually answered are written: replaying a cache hit
// back into the cache would refresh a lifetime that is meant to expire. Only
// cacheable turns are written, and only output that parses — see
// validateGeneratedPart. Every failure here is logged and swallowed: a cache
// that cannot be filled is slow, not broken.
func (l *ServiceImpl) persistGenerations(
	ctx context.Context,
	cc *common.ChatContext,
	plan []partPlan,
	rawResponses map[string]string,
	cityID uuid.UUID,
) {
	var cityIDPtr *uuid.UUID
	if cityID != uuid.Nil {
		id := cityID
		cityIDPtr = &id
	}
	now := time.Now()

	for _, p := range plan {
		name := string(p.Part)
		outcome := cc.PartOutcomes[name]

		l.captureGenerationServed(cc, p, outcome)

		if outcome.ServedFrom != servedFromLLM || !p.Cacheable || p.CacheKey == "" {
			continue
		}
		text := rawResponses[name]
		if !validateGeneratedPart(p.Part, text) {
			// Counted, because a part that keeps failing validation is a
			// prompt or a model problem, and it will miss forever otherwise.
			observability.RecordLLMCacheLookup(name,
				observability.LLMCacheLayerNone, observability.LLMCacheResultInvalid)
			l.logger.WarnContext(ctx, "generated part failed validation; not caching",
				slog.String("part", name), slog.Int("length", len(text)))
			continue
		}

		if l.cache != nil {
			l.cache.Set(p.CacheKey, text, min(p.TTL, maxMemoryGenerationTTL))
		}
		if l.generations == nil {
			continue
		}
		if err := l.generations.PutGeneration(ctx, locitypes.LLMGeneration{
			CacheKey:        p.CacheKey,
			TemplateVersion: generationTemplateVersion,
			Part:            name,
			Domain:          string(cc.Domain),
			ModelID:         outcome.ModelID,
			ModelVersion:    outcome.ModelVersion,
			PromptHash:      outcome.PromptHash,
			City:            normalizeCacheComponent(cc.CityName),
			CityID:          cityIDPtr,
			Response:        text,
			// The packet is not in the key — evidence churn would miss on
			// every ingest — so it is recorded next to the answer instead, and
			// staleness is bounded by the part's TTL.
			PacketID:  packetIDFor(cc),
			TokensIn:  outcome.TokensIn,
			TokensOut: outcome.TokensOut,
			ExpiresAt: now.Add(p.TTL),
		}); err != nil {
			l.logger.WarnContext(ctx, "failed to store generation",
				slog.String("part", name), slog.Any("error", err))
		}
	}
}

// captureGenerationServed reports one part to PostHog: which layer answered
// it, on which model, and how many output tokens the hit saved. Hit rate per
// part is count(layer != none) / count().
func (l *ServiceImpl) captureGenerationServed(cc *common.ChatContext, p partPlan, outcome common.PartOutcome) {
	if l.analytics == nil || outcome.ServedFrom == "" {
		return
	}
	layer := outcome.ServedFrom
	saved := outcome.TokensOut
	if layer == servedFromLLM {
		// Nothing was saved: this part was paid for.
		layer = observability.LLMCacheLayerNone
		saved = 0
	}
	l.analytics.Capture(cc.UserID.String(), "llm_generation_served", map[string]any{
		"part":                string(p.Part),
		"layer":               layer,
		"model":               outcome.ModelID,
		"saved_output_tokens": saved,
		"domain":              string(cc.Domain),
		"city":                cc.CityName,
		"session_id":          cc.SessionID.String(),
		"cache_key":           p.CacheKey,
	})
}

// buildInteractionRow assembles the audit row for one turn.
//
// It records what the request was planned on and what it actually cost, so the
// row can answer "was this answer generated or replayed, by which model, for
// how many tokens" — questions the old row, which wrote a static model name
// and left every cache and token column null, could not.
func (l *ServiceImpl) buildInteractionRow(
	cc *common.ChatContext,
	plan []partPlan,
	fullResponse string,
	startTime time.Time,
) locitypes.LlmInteraction {
	modelID := l.model
	if len(plan) > 0 && plan[0].ModelID != "" {
		modelID = plan[0].ModelID
	}

	var tokensIn, tokensOut int
	cacheHit := len(plan) > 0
	parts := make(map[string]any, len(plan))
	hashes := make([]string, 0, len(plan))
	for _, p := range plan {
		name := string(p.Part)
		outcome := cc.PartOutcomes[name]
		tokensIn += outcome.TokensIn
		tokensOut += outcome.TokensOut
		if outcome.ServedFrom != observability.LLMCacheLayerMemory && outcome.ServedFrom != observability.LLMCacheLayerDB {
			cacheHit = false
		}
		parts[name] = map[string]any{
			"served_from":   outcome.ServedFrom,
			"cache_key":     outcome.CacheKey,
			"model":         outcome.ModelID,
			"model_version": outcome.ModelVersion,
			"tokens_out":    outcome.TokensOut,
		}
		// Cached parts rendered no prompt this turn; their key stands in, so
		// the digest still changes when the set of parts does.
		if outcome.PromptHash != "" {
			hashes = append(hashes, name+"="+outcome.PromptHash)
		} else {
			hashes = append(hashes, name+"=cached:"+p.CacheKey)
		}
	}

	interaction := locitypes.LlmInteraction{
		ID:               uuid.New(),
		SessionID:        cc.SessionID,
		UserID:           cc.UserID,
		ProfileID:        cc.ProfileID,
		CityName:         cc.CityName,
		Prompt:           fmt.Sprintf("Unified Chat Stream - Domain: %s, Message: %s", cc.Domain, cc.Message),
		ResponseText:     fullResponse,
		ModelUsed:        modelID,
		LatencyMs:        int(time.Since(startTime).Milliseconds()),
		Timestamp:        startTime,
		CacheKey:         cc.CacheKey,
		CacheHit:         cacheHit,
		PromptHash:       combinedPromptHash(hashes),
		Provider:         providerForModel(modelID, l.provider),
		PromptTokens:     tokensIn,
		CompletionTokens: tokensOut,
		TotalTokens:      tokensIn + tokensOut,
		IsStreaming:      true,
	}
	if payload, err := json.Marshal(map[string]any{"parts": parts}); err == nil {
		interaction.ResponsePayload = payload
	}
	return interaction
}

// combinedPromptHash digests every part's prompt fingerprint into the one
// value the interaction row has room for.
func combinedPromptHash(hashes []string) string {
	if len(hashes) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(hashes, "\x00")))
	return hex.EncodeToString(sum[:])
}

// providerForModel names the upstream that answered, from the model id when
// the id says so and from the configured provider otherwise.
//
// The column used to default to 'google' for everything, which quietly
// mis-attributed every DeepSeek and OpenRouter call in the cost reports. An
// unknown provider is left empty rather than guessed.
func providerForModel(modelID, configured string) string {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return configured
	}
	// OpenRouter-style "vendor/model" ids name the vendor outright.
	if vendor, _, ok := strings.Cut(id, "/"); ok && vendor != "" {
		return vendor
	}
	switch {
	case strings.HasPrefix(id, "gemini"):
		return "google"
	case strings.HasPrefix(id, "gpt"), strings.HasPrefix(id, "o1"), strings.HasPrefix(id, "o3"):
		return "openai"
	case strings.HasPrefix(id, "claude"):
		return "anthropic"
	case strings.HasPrefix(id, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(id, "mistral"), strings.HasPrefix(id, "mixtral"):
		return "mistralai"
	case strings.HasPrefix(id, "llama"):
		return "meta"
	case strings.HasPrefix(id, "grok"):
		return "xai"
	}
	return configured
}
