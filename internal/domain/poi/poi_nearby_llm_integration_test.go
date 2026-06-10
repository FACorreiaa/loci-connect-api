package poi

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"google.golang.org/genai"
)

// TestNearbyLLMReturnsPOIs is a real-LLM integration test for the near-me bug:
// the model used to return a bare "null" for these coordinates, yielding zero
// POIs. It verifies the current prompt produces a parseable, non-empty result.
// Skips unless GEMINI_API_KEY is set.
func TestNearbyLLMReturnsPOIs(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set, skipping real-LLM integration test")
	}

	ctx := context.Background()
	client, err := generativeAI.NewGeminiChatClient(ctx, apiKey, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// The exact coordinates from the bug report (near Braga, Portugal), 25km.
	const lat, lon, distance = 41.48395992358889, -8.776363031387108, 25000.0
	prompt := getGeneralPOIByDistancePrompt(lat, lon, distance, false)

	resp, err := client.Generate(ctx, prompt, &genai.GenerateContentConfig{
		Temperature:     genai.Ptr[float32](0.7),
		MaxOutputTokens: 16384,
	})
	if err != nil {
		t.Fatalf("GenerateResponse error: %v", err)
	}

	var txt string
	for _, c := range resp.Candidates {
		if c.Content != nil && len(c.Content.Parts) > 0 {
			txt = c.Content.Parts[0].Text
			break
		}
	}
	if txt == "" {
		t.Fatal("empty response text from model")
	}

	clean := cleanJSONResponse(txt)
	if clean == "" {
		t.Fatalf("cleaned response is empty (model returned null/empty); raw=%q", txt)
	}

	var poiData struct {
		PointsOfInterest []struct {
			Name string `json:"name"`
		} `json:"points_of_interest"`
	}
	if err := json.Unmarshal([]byte(clean), &poiData); err != nil {
		t.Fatalf("failed to parse cleaned response: %v; clean=%q", err, clean)
	}
	if len(poiData.PointsOfInterest) == 0 {
		t.Fatalf("expected at least 1 POI, got 0; clean=%q", clean)
	}
	t.Logf("model returned %d POIs (first: %q)", len(poiData.PointsOfInterest), poiData.PointsOfInterest[0].Name)
}

// TestDomainNearbyPromptsReturnPOIs verifies the per-domain prompts
// (restaurants/hotels/activities/attractions) now emit the points_of_interest
// contract the workers parse, with a correct km radius. Before the fix they
// emitted a mismatched JSON key and treated the meter distance as km, so the
// LLM fallback always parsed to zero. Skips unless GEMINI_API_KEY is set.
func TestDomainNearbyPromptsReturnPOIs(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set, skipping real-LLM integration test")
	}

	ctx := context.Background()
	client, err := generativeAI.NewGeminiChatClient(ctx, apiKey, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	const lat, lon, distance = 41.48395992358889, -8.776363031387108, 25000.0
	cases := map[string]string{
		"restaurants": getRestaurantsNearbyPrompt(lat, lon, distance),
		"hotels":      getHotelsNeabyPrompt(lat, lon, distance),
		"activities":  getActivitiesNearbyPrompt(lat, lon, distance),
		"attractions": getAttractionsNeabyPrompt(lat, lon, distance),
	}

	for domain, prompt := range cases {
		t.Run(domain, func(t *testing.T) {
			resp, err := client.Generate(ctx, prompt, &genai.GenerateContentConfig{
				Temperature:     genai.Ptr[float32](0.7),
				MaxOutputTokens: 16384,
			})
			if err != nil {
				t.Fatalf("GenerateResponse error: %v", err)
			}
			var txt string
			for _, c := range resp.Candidates {
				if c.Content != nil && len(c.Content.Parts) > 0 {
					txt = c.Content.Parts[0].Text
					break
				}
			}
			clean := cleanJSONResponse(txt)
			if clean == "" {
				t.Fatalf("%s: cleaned response empty (model returned null/empty); raw=%q", domain, txt)
			}
			var poiData struct {
				PointsOfInterest []struct {
					Name string `json:"name"`
				} `json:"points_of_interest"`
			}
			if err := json.Unmarshal([]byte(clean), &poiData); err != nil {
				t.Fatalf("%s: parse failed: %v; clean=%q", domain, err, clean)
			}
			if len(poiData.PointsOfInterest) == 0 {
				t.Fatalf("%s: expected >=1 POI, got 0; clean=%q", domain, clean)
			}
			t.Logf("%s: %d POIs (first: %q)", domain, len(poiData.PointsOfInterest), poiData.PointsOfInterest[0].Name)
		})
	}
}
