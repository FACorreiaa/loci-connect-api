package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
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

// extractCityFromMessage uses AI to extract city name and clean the message

func (l *ServiceImpl) extractCityFromMessage(ctx context.Context, message string) (cityName, cleanedMessage string, err error) {
	prompt := fmt.Sprintf(`
You are a text parser. Extract the city name from the user's travel request and return a clean version of the message.

User message: "%s"

Respond with ONLY a JSON object in this exact format:
{
    "city": "City Name",
    "message": "cleaned message without city"
}

Examples:
- "Find restaurants in Barcelona" → {"city": "Barcelona", "message": "Find restaurants"}
- "What to do in Paris?" → {"city": "Paris", "message": "What to do"}
- "Barcelona restaurants" → {"city": "Barcelona", "message": "restaurants"}
- "Show me hotels in New York" → {"city": "New York", "message": "Show me hotels"}
- "Things to do Madrid" → {"city": "Madrid", "message": "Things to do"}

If no city is mentioned, use empty string for city.
`, message)

	release, err := l.acquireLLMSlot(ctx)
	if err != nil {
		return "", "", fmt.Errorf("LLM capacity exceeded: %w", err)
	}
	defer release()

	response, err := l.aiClient.Generate(ctx, prompt, &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.1), // Low temperature for consistent parsing
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to parse message: %w", err)
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
		return "", "", fmt.Errorf("empty response from AI parser")
	}

	cleanResponse := generativeAI.CleanJSON(responseText.String())
	var parsed struct {
		City    string `json:"city"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(cleanResponse), &parsed); err != nil {
		return "", "", fmt.Errorf("failed to parse extraction response: %w", err)
	}

	// If no city extracted, return original message
	if parsed.City == "" {
		return "", message, nil
	}

	return parsed.City, parsed.Message, nil
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
