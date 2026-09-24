package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
)

// ExtractedCity is one city read out of a request, with the days the
// traveller gave it (0 when they did not say).
type ExtractedCity struct {
	Name string `json:"city"`
	Days int    `json:"days,omitempty"`
}

// TripCities is everything the extractor reads out of one message. One city is
// today's single-city request; two or more is a multi-city trip. Ordered says
// the traveller sequenced them themselves ("then", "after that", "finishing
// in") rather than listing candidates.
type TripCities struct {
	Cities  []ExtractedCity `json:"cities"`
	Ordered bool            `json:"ordered"`
	Message string          `json:"message"`
}

// First is the single city a single-city caller wants, or "".
func (t TripCities) First() string {
	if len(t.Cities) == 0 {
		return ""
	}
	return t.Cities[0].Name
}

// parseTripCities reads the extractor's JSON. It also accepts the old
// {"city","message"} shape, so an answer in that shape from a model that
// ignored the new format still works.
func parseTripCities(raw, original string) (TripCities, error) {
	clean := generativeAI.CleanJSON(raw)
	var parsed struct {
		Cities  []ExtractedCity `json:"cities"`
		Ordered bool            `json:"ordered"`
		Message string          `json:"message"`
		City    string          `json:"city"`
	}
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return TripCities{}, fmt.Errorf("failed to parse extraction response: %w", err)
	}
	out := TripCities{Ordered: parsed.Ordered, Message: parsed.Message}
	in := parsed.Cities
	if len(in) == 0 && strings.TrimSpace(parsed.City) != "" {
		in = []ExtractedCity{{Name: parsed.City}}
	}
	seen := map[string]bool{}
	for _, c := range in {
		name := strings.TrimSpace(c.Name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		if c.Days < 0 {
			c.Days = 0
		}
		out.Cities = append(out.Cities, ExtractedCity{Name: name, Days: c.Days})
	}
	if len(out.Cities) == 0 || strings.TrimSpace(out.Message) == "" {
		out.Message = original
	}
	return out, nil
}

// tripCitiesCacheKey is the extraction cache key for the list-shaped answer.
// A new prefix, so entries from the single-city extractor are not misread.
func tripCitiesCacheKey(message string) string { return "tripcities:" + extractionCacheKey(message) }

// extractTripCitiesCached is the city extractor behind a global, 24-hour
// cache: one provider call per distinct message, shared by everyone. See
// extractCityCached for why sharing it is safe.
func (l *ServiceImpl) extractTripCitiesCached(ctx context.Context, message string) (TripCities, error) {
	key := tripCitiesCacheKey(message)
	if raw, ok := l.cachedText(key); ok {
		var tc TripCities
		if json.Unmarshal([]byte(raw), &tc) == nil {
			return tc, nil
		}
	}
	tc, err := l.extractTripCitiesFromMessage(ctx, message)
	if err != nil {
		return TripCities{}, err
	}
	if raw, mErr := json.Marshal(tc); mErr == nil && l.cache != nil {
		l.cache.Set(key, string(raw), extractionCacheTTL)
	}
	return tc, nil
}
