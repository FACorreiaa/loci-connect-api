package service

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func normalizeCacheComponent(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// extractTripCitiesFromMessage uses AI to read every city out of a travel
// request, in the order given, and clean the message of them.
func (l *ServiceImpl) extractTripCitiesFromMessage(ctx context.Context, message string) (TripCities, error) {
	prompt := fmt.Sprintf(`
You are a text parser. Read every city the traveller wants to visit, in the order they gave them, and return a clean version of the message with the city names and per-city durations removed.

User message: "%s"

Respond with ONLY a JSON object in this exact format:
{
    "cities": [{"city": "City Name", "days": 0}],
    "ordered": false,
    "message": "cleaned message without cities"
}

"days" is how many days or nights the traveller gave that one city, or 0 if they did not say.
"ordered" is true only when they sequenced the cities themselves ("then", "after", "ending in", "first ... then").
A neighbourhood, landmark or region is not a city. Never invent a city.

Examples:
- "Find restaurants in Barcelona" → {"cities":[{"city":"Barcelona","days":0}],"ordered":false,"message":"Find restaurants"}
- "Lisbon for 3 days then Porto for 2" → {"cities":[{"city":"Lisbon","days":3},{"city":"Porto","days":2}],"ordered":true,"message":"trip"}
- "A week in Lisbon, Porto and Seville" → {"cities":[{"city":"Lisbon","days":0},{"city":"Porto","days":0},{"city":"Seville","days":0}],"ordered":false,"message":"A week trip"}
- "What to do in Paris?" → {"cities":[{"city":"Paris","days":0}],"ordered":false,"message":"What to do"}
- "Replace Alfama with Belém" → {"cities":[],"ordered":false,"message":"Replace Alfama with Belém"}

If no city is mentioned, return an empty "cities" list.
`, message)

	release, err := l.acquireLLMSlot(ctx)
	if err != nil {
		return TripCities{}, fmt.Errorf("LLM capacity exceeded: %w", err)
	}
	defer release()

	response, err := l.aiClient.Generate(ctx, prompt, &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.1), // Low temperature for consistent parsing
	})
	if err != nil {
		return TripCities{}, fmt.Errorf("failed to parse message: %w", err)
	}

	var responseText strings.Builder
	for _, cand := range response.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					responseText.WriteString(part.Text)
				}
			}
		}
	}

	if responseText.String() == "" {
		return TripCities{}, fmt.Errorf("empty response from AI parser")
	}

	return parseTripCities(responseText.String(), message)
}

// convertHotelsToPOIs adapts hotel details into POI entries for client responses.

// extractJSONFromMarkdown extracts JSON content from markdown code blocks

func extractJSONFromMarkdown(content string) string {
	// Remove markdown code block delimiters
	lines := strings.Split(content, "\n")
	var jsonLines []string
	inCodeBlock := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "```json" || trimmedLine == "```" {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock || (!strings.HasPrefix(trimmedLine, "```") && (strings.HasPrefix(trimmedLine, "{") || strings.HasPrefix(trimmedLine, "[") || len(jsonLines) > 0)) {
			jsonLines = append(jsonLines, line)
		}
	}

	result := strings.Join(jsonLines, "\n")
	result = strings.TrimSpace(result)

	// If no JSON was extracted, return the original content
	if result == "" {
		return strings.TrimSpace(content)
	}

	return result
}

// convertHotelsToPOIs and convertRestaurantsToPOIs live in the types package
// now, so the presenter can put the same lists into AiCityResponse.
func convertHotelsToPOIs(hotels []locitypes.HotelDetailedInfo) []locitypes.POIDetailedInfo {
	return locitypes.HotelsToPOIs(hotels)
}

func convertRestaurantsToPOIs(restaurants []locitypes.RestaurantDetailedInfo) []locitypes.POIDetailedInfo {
	return locitypes.RestaurantsToPOIs(restaurants)
}
