package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
	"github.com/FACorreiaa/loci-connect-api/pkg/tripspan"
)

// servedFromLLM marks a part the provider answered. The cache layers use the
// observability layer names, so an outcome's ServedFrom doubles as the label
// the metric and the analytics event carry.
const servedFromLLM = "llm"

// maxMemoryGenerationTTL caps how long a generation may sit in the in-process
// store. go-cache is unbounded, so an itinerary with a 14-day durable life
// would otherwise pin a pod's memory for 14 days; the database is where
// durability lives.
const maxMemoryGenerationTTL = time.Hour

// replayChunkSize and replayChunkPace shape a cache replay. The client's
// renderer wants chunks rather than one 30 KB event, but the old 100-byte /
// 10 ms pacing spent three seconds re-typing an answer that was already in
// memory. A kilobyte every two milliseconds still streams, and finishes in
// tens of milliseconds.
const (
	replayChunkSize = 1024
	replayChunkPace = 2 * time.Millisecond
)

// Output budget for one part.
//
// The streaming path used to send no MaxOutputTokens at all, which was fine
// while an answer was ten places and is not fine at fifty: that is past the
// 8192-token default several providers apply, and a response cut off
// mid-array parses as a shorter list rather than as an error. Sizing the
// budget to what was asked for is what keeps a truncation impossible rather
// than merely unlikely.
const (
	baseOutputTokens   = 2048
	tokensPerPlace     = 320
	maxOutputTokensCap = 32768
)

func outputTokenBudget(target int) int32 {
	budget := baseOutputTokens + tokensPerPlace*target
	if budget > maxOutputTokensCap {
		budget = maxOutputTokensCap
	}
	return int32(budget)
}

// partPlan is everything needed to produce one part of an answer: where a
// cached copy would live, how to render the prompt if there is none, and what
// happened when the plan was looked up.
//
// The plan is built once per request and handed down the call chain —
// planning, lookup, streaming and persistence all read the same values, so the
// key a part was looked up under is by construction the key it is written back
// to.
type partPlan struct {
	Part generationPart
	// CacheKey identifies this part's answer. Empty only when the part is not
	// cacheable at all.
	CacheKey string
	// TTL is how long a fresh answer for this part stays servable.
	TTL time.Duration
	// Cacheable is this turn's verdict (see common.ChatContext.Cacheable),
	// copied per part so the lookup and the write path agree without reaching
	// back for the context.
	Cacheable bool
	// ModelID is the model the request is planned to run on. It is in the key,
	// so a plan change or a BYOK switch cannot replay another model's answer.
	ModelID string
	// Prompt renders this part's prompt against the evidence packet. It closes
	// over the part's scoped preference text, which is the same value its
	// snapshot hash was taken from.
	Prompt func(packet *retrieval.ContextPacket) string
	// POITarget is how many places this part was asked for. It sizes the
	// output budget: the JSON for fifty places does not fit in a default
	// response, and a truncated one is indistinguishable from a short answer
	// by the time it reaches the parser.
	POITarget int
	// Hit is the cached answer when the lookup found one, nil on a miss.
	Hit *cachedPart
}

// cachedPart is an answer that was found rather than generated.
type cachedPart struct {
	Text string
	// Layer is where it was found: observability.LLMCacheLayerMemory or
	// LLMCacheLayerDB.
	Layer string
	// TokensOut is what the original generation cost, carried so the analytics
	// event can report what this hit saved. Zero for a memory hit, which holds
	// the text alone.
	TokensOut int
	// ModelVersion is the model that produced the stored answer, when the
	// durable row recorded one.
	ModelVersion string
}

// planGeneration builds one partPlan per part of this turn's domain.
//
// Prompt and key are derived from the same scoped profile: scopeProfileForPart
// reduces the profile to the fields that part's template renders, that value
// is rendered into the prompt and hashed into the key, so the key depends on
// the prompt's inputs by construction rather than by a second allowlist that
// could drift from it.
//
// Returns nil for the nearby domain, which is answered from PostGIS, and for
// any domain with no parts.
func (l *ServiceImpl) planGeneration(cc *common.ChatContext) []partPlan {
	modelID := l.modelFor(cc.Ctx)

	// assumedDays says the horizon was a fallback rather than something the
	// traveller stated, which changes what the prompt asks for: a sample of
	// the iconic rather than a filled-in itinerary.
	assumedDays := cc.TripDaysSource == string(tripspan.SourceDefault)

	var lat, lon float64
	hasLocation := cc.UserLocation != nil
	if hasLocation {
		lat, lon = cc.UserLocation.UserLat, cc.UserLocation.UserLon
	}

	part := func(p generationPart, render func(prefs string, packet *retrieval.ContextPacket) string) partPlan {
		scoped := scopeProfileForPart(p, cc.Profile)
		prefs := getUserPreferencesPrompt(scoped)
		return partPlan{
			Part:      p,
			POITarget: cc.POITarget,
			CacheKey: buildGenerationKey(generationKeyInput{
				Part:         p,
				Domain:       cc.Domain,
				CityID:       cc.CityID,
				CityName:     cc.CityName,
				ModelID:      modelID,
				Query:        cc.Message,
				SnapshotHash: profileSnapshotHash(scoped),
				UserID:       cc.UserID,
				Lat:          lat,
				Lon:          lon,
				HasLocation:  hasLocation,
				POITarget:    cc.POITarget,
			}),
			TTL:       partTTL(p),
			Cacheable: cc.Cacheable,
			ModelID:   modelID,
			Prompt: func(packet *retrieval.ContextPacket) string {
				return render(prefs, packet)
			},
		}
	}

	cityData := part(partCityData, func(_ string, _ *retrieval.ContextPacket) string {
		return getCityDataPrompt(cc.CityName)
	})

	switch cc.Domain {
	case locitypes.DomainItinerary, locitypes.DomainGeneral:
		return []partPlan{
			cityData,
			// The shared POI list is grounded in the depersonalised packet: a
			// global answer must never carry one traveller's visited flags.
			part(partGeneralPOIs, func(_ string, packet *retrieval.ContextPacket) string {
				return groundPrompt(getGeneralPOIPrompt(cc.CityName, cc.Message, cc.POITarget, cc.TripDays, assumedDays), packet.WithoutPersonal())
			}),
			part(partItinerary, func(prefs string, packet *retrieval.ContextPacket) string {
				return groundPrompt(getPersonalizedItineraryPrompt(cc.CityName, cc.Message, prefs, cc.POITarget, cc.TripDays, assumedDays), packet)
			}),
		}
	case locitypes.DomainAccommodation:
		return []partPlan{
			cityData,
			part(partHotels, func(prefs string, packet *retrieval.ContextPacket) string {
				return groundPrompt(getAccommodationPrompt(cc.CityName, lat, lon, cc.Message, prefs), packet)
			}),
		}
	case locitypes.DomainDining:
		return []partPlan{
			cityData,
			part(partRestaurants, func(prefs string, packet *retrieval.ContextPacket) string {
				return groundPrompt(getDiningPrompt(cc.CityName, lat, lon, cc.Message, prefs, cc.POITarget, cc.TripDays, assumedDays), packet)
			}),
		}
	case locitypes.DomainActivities:
		return []partPlan{
			cityData,
			part(partActivities, func(prefs string, packet *retrieval.ContextPacket) string {
				return groundPrompt(getActivitiesPrompt(cc.CityName, lat, lon, cc.Message, prefs, cc.POITarget, cc.TripDays, assumedDays), packet)
			}),
		}
	default:
		return nil
	}
}

// lookupGenerations resolves each planned part against the cache layers and
// returns how many were found.
//
// Memory first, then the durable table; a durable hit repopulates memory so
// the next request on this pod does not pay the round trip. Every part is
// counted exactly once, with the layer that answered it — or "none" with
// result "bypass" for a part this turn is not allowed to serve from cache.
//
// A nil generation store means the durable layer is off (the kill-switch, and
// every unit test): memory is then the only layer.
func (l *ServiceImpl) lookupGenerations(ctx context.Context, plan []partPlan) int {
	hits := 0
	for i := range plan {
		p := &plan[i]
		name := string(p.Part)

		if !p.Cacheable || p.CacheKey == "" {
			observability.RecordLLMCacheLookup(name,
				observability.LLMCacheLayerNone, observability.LLMCacheResultBypass)
			continue
		}

		if text, ok := l.cachedText(p.CacheKey); ok {
			p.Hit = &cachedPart{Text: text, Layer: observability.LLMCacheLayerMemory}
			observability.RecordLLMCacheLookup(name,
				observability.LLMCacheLayerMemory, observability.LLMCacheResultHit)
			hits++
			continue
		}

		if l.generations == nil {
			observability.RecordLLMCacheLookup(name,
				observability.LLMCacheLayerNone, observability.LLMCacheResultMiss)
			continue
		}

		row, err := l.generations.GetGeneration(ctx, p.CacheKey)
		if err != nil {
			// A cache that cannot be read is a miss, never a failed request.
			l.logger.WarnContext(ctx, "durable generation lookup failed",
				slog.String("part", name), slog.Any("error", err))
			observability.RecordLLMCacheLookup(name,
				observability.LLMCacheLayerDB, observability.LLMCacheResultMiss)
			continue
		}
		// The store's own query hides expired rows; the expiry check is
		// repeated here so a store that does not (a fake, a future backend)
		// cannot serve a stale answer, and so the memory TTL below is always
		// positive.
		if row == nil || row.Response == "" || !row.ExpiresAt.After(time.Now()) {
			observability.RecordLLMCacheLookup(name,
				observability.LLMCacheLayerDB, observability.LLMCacheResultMiss)
			continue
		}

		p.Hit = &cachedPart{
			Text:         row.Response,
			Layer:        observability.LLMCacheLayerDB,
			TokensOut:    row.TokensOut,
			ModelVersion: row.ModelVersion,
		}
		if ttl := memoryGenerationTTL(p.TTL, row.ExpiresAt); ttl > 0 && l.cache != nil {
			l.cache.Set(p.CacheKey, row.Response, ttl)
		}
		observability.RecordLLMCacheLookup(name,
			observability.LLMCacheLayerDB, observability.LLMCacheResultHit)
		hits++
	}
	return hits
}

// cachedText reads a non-empty string from the in-process store. Values that
// are not strings, or are empty, are treated as absent: they cannot be
// replayed as an answer.
func (l *ServiceImpl) cachedText(key string) (string, bool) {
	if l.cache == nil || key == "" {
		return "", false
	}
	v, found := l.cache.Get(key)
	if !found {
		return "", false
	}
	text, ok := v.(string)
	if !ok || text == "" {
		return "", false
	}
	return text, true
}

// memoryGenerationTTL is how long a durable answer may be mirrored in memory:
// no longer than the part's own lifetime, no longer than what is left of the
// row's, and never more than an hour.
func memoryGenerationTTL(partLifetime time.Duration, expiresAt time.Time) time.Duration {
	return min(partLifetime, time.Until(expiresAt), maxMemoryGenerationTTL)
}

// requestCacheKey is the whole request's identity for the start event and the
// interaction row: a short digest over every part key, so one value still
// distinguishes two requests that differ in any part.
func requestCacheKey(plan []partPlan) string {
	if len(plan) == 0 {
		return ""
	}
	keys := make([]string, 0, len(plan))
	for _, p := range plan {
		keys = append(keys, p.CacheKey)
	}
	sum := sha256.Sum256([]byte(strings.Join(keys, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

// promptHash fingerprints a rendered prompt for the audit trail, so a stored
// answer can be tied to the exact text that produced it.
func promptHash(prompt string) string {
	if prompt == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// startPartSpan opens the span that covers producing one part, whichever layer
// produces it. Provider spans nest under it on a miss, so a trace shows either
// a cheap replay or the call it replaced.
func startPartSpan(ctx context.Context, p partPlan, layer string) (context.Context, trace.Span) {
	return otel.Tracer("LlmInteractionService").Start(ctx, "generate "+string(p.Part),
		trace.WithAttributes(
			attribute.String("loci.part", string(p.Part)),
			attribute.String("loci.cache_layer", layer),
			attribute.String("loci.cache_key", p.CacheKey),
			attribute.String("gen_ai.request.model", p.ModelID),
		))
}

// replayCachedPart streams an answer that was already produced.
//
// It emits the same chunk events a live generation does — the client cannot
// tell the two apart beyond the cache_used flag it already reads — plus
// cache_layer, so a client or a log can say which layer answered.
func (l *ServiceImpl) replayCachedPart(
	ctx context.Context,
	p partPlan,
	sendEvent func(locitypes.StreamEvent),
	domain locitypes.DomainType,
) error {
	ctx, span := startPartSpan(ctx, p, p.Hit.Layer)
	defer span.End()

	text := p.Hit.Text
	l.logger.InfoContext(ctx, "serving generation from cache",
		slog.String("part", string(p.Part)),
		slog.String("layer", p.Hit.Layer),
		slog.String("cache_key", p.CacheKey))

	for i := 0; i < len(text); i += replayChunkSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := min(i+replayChunkSize, len(text))
		sendEvent(locitypes.StreamEvent{
			Type: locitypes.EventTypeChunk,
			Data: map[string]any{
				"part":        string(p.Part),
				"chunk":       text[i:end],
				"domain":      string(domain),
				"cache_key":   p.CacheKey,
				"cache_used":  true,
				"cache_layer": p.Hit.Layer,
			},
		})
		time.Sleep(replayChunkPace)
	}
	return nil
}

// streamPartFromLLM streams one part from the provider and returns what it
// produced, including the model that answered and what it spent.
//
// It neither reads nor writes a cache: the lookup happened before retrieval
// ran (lookupGenerations) and the write happens after the output has been
// validated (persistGenerations). Caching an unparseable stream here was how
// invalid JSON used to be replayed for five minutes.
func (l *ServiceImpl) streamPartFromLLM(
	ctx context.Context,
	p partPlan,
	prompt string,
	sendEvent func(locitypes.StreamEvent),
	domain locitypes.DomainType,
) (streamResult, error) {
	partType := string(p.Part)
	ctx, span := startPartSpan(ctx, p, observability.LLMCacheLayerNone)
	defer span.End()

	l.logger.InfoContext(ctx, "Calling LLM for streaming",
		slog.String("part_type", partType),
		slog.String("cache_key", p.CacheKey),
		slog.Int("prompt_length", len(prompt)))

	release, err := l.acquireLLMSlot(ctx)
	if err != nil {
		l.logger.ErrorContext(ctx, "LLM capacity exceeded",
			slog.String("part_type", partType),
			slog.Any("error", err))
		if ctx.Err() == nil {
			sendEvent(locitypes.StreamEvent{
				Type:  locitypes.EventTypeError,
				Error: "We are experiencing high traffic. Please try again in a minute.",
			})
		}
		return streamResult{}, err
	}
	defer release()

	iter, err := l.aiClient.GenerateStream(ctx, prompt, &genai.GenerateContentConfig{
		Temperature:     genai.Ptr[float32](defaultTemperature),
		MaxOutputTokens: outputTokenBudget(p.POITarget),
	})
	if err != nil {
		l.logger.ErrorContext(ctx, "LLM stream call failed",
			slog.String("part_type", partType),
			slog.Any("error", err))
		if ctx.Err() == nil {
			errorMsg := fmt.Sprintf("%s worker failed: %v", partType, err)
			// Classify from the typed sentinels rather than the error text
			// and pass the verdict along, so the transport does not have to
			// re-derive it by matching on user-facing prose.
			var errorCode locitypes.StreamErrorCode
			switch {
			case errors.Is(err, llmerrors.ErrRateLimited):
				errorCode = locitypes.StreamErrorQuotaExceeded
				errorMsg = "We are experiencing high traffic (Quota Exceeded). Please try again in a minute."
			case errors.Is(err, llmerrors.ErrOutOfCredits), errors.Is(err, llmerrors.ErrAuthFailed):
				// Every provider in the chain was exhausted or rejected.
				// Retryable from the client's point of view, but it needs
				// an operator to actually clear.
				errorCode = locitypes.StreamErrorProviderUnavailable
				errorMsg = "The AI service is temporarily unavailable. Please try again later."
			case errors.Is(err, llmerrors.ErrUnavailable):
				errorCode = locitypes.StreamErrorProviderUnavailable
				errorMsg = "The AI service is temporarily unavailable. Please try again in a moment."
			}

			sendEvent(locitypes.StreamEvent{
				Type:      locitypes.EventTypeError,
				Error:     errorMsg,
				ErrorCode: errorCode,
			})
		}
		return streamResult{}, fmt.Errorf("%s worker failed: %w", partType, err)
	}

	var fullResponse strings.Builder
	var result streamResult
	chunkCount := 0
	for resp, err := range iter {
		if ctx.Err() != nil {
			l.logger.WarnContext(ctx, "Context canceled during streaming",
				slog.String("part_type", partType),
				slog.Int("chunks_received", chunkCount))
			return streamResult{}, ctx.Err()
		}
		if err != nil {
			l.logger.ErrorContext(ctx, "Streaming error from LLM",
				slog.String("part_type", partType),
				slog.Any("error", err))
			if ctx.Err() == nil {
				sendEvent(locitypes.StreamEvent{
					Type:  locitypes.EventTypeError,
					Error: fmt.Sprintf("%s streaming error: %v", partType, err),
				})
			}
			return streamResult{}, fmt.Errorf("%s streaming error: %w", partType, err)
		}
		// Providers name the answering model on some or all chunks and
		// report usage on the last one, cumulatively; keep the latest of
		// each rather than summing what would then be counted twice.
		if resp.ModelVersion != "" {
			result.ModelVersion = resp.ModelVersion
		}
		if u := resp.UsageMetadata; u != nil {
			result.TokensIn = int(u.PromptTokenCount)
			result.TokensOut = int(u.CandidatesTokenCount)
		}
		for _, cand := range resp.Candidates {
			if cand.Content == nil {
				continue
			}
			for _, part := range cand.Content.Parts {
				if part.Text == "" {
					continue
				}
				chunk := part.Text
				chunkCount++
				fullResponse.WriteString(chunk)

				// Debug-only, and without a content preview: the chunk is
				// user-influenced model output and must not land in Info logs.
				if chunkCount <= 3 {
					l.logger.DebugContext(ctx, "Received chunk from LLM",
						slog.String("part_type", partType),
						slog.Int("chunk_number", chunkCount),
						slog.Int("chunk_length", len(chunk)))
				}

				sendEvent(locitypes.StreamEvent{
					Type: locitypes.EventTypeChunk,
					Data: map[string]any{
						"part":        partType,
						"chunk":       chunk,
						"domain":      string(domain),
						"cache_key":   p.CacheKey,
						"cache_used":  false,
						"cache_layer": observability.LLMCacheLayerNone,
					},
				})
			}
		}
	}

	l.logger.InfoContext(ctx, "LLM streaming completed",
		slog.String("part_type", partType),
		slog.Int("total_chunks", chunkCount),
		slog.Int("total_response_length", fullResponse.Len()))

	result.Text = fullResponse.String()
	return result, nil
}
