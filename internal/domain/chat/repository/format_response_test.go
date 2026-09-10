package repository

import (
	"strings"
	"testing"
)

const (
	testCityDataJSON = `{"city":"Funchal","country":"Portugal","description":"Capital of Madeira.","population":"105,000","language":"Portuguese","weather":"Mild","attractions":"Monte Palace","history":"Founded in 1421."}`

	testGeneralPOIsJSON = `{"points_of_interest":[{"name":"Monte Palace Tropical Garden","category":"Garden","latitude":32.675,"longitude":-16.9},{"name":"Mercado dos Lavradores","category":"Market","latitude":32.648,"longitude":-16.905}]}`

	testItineraryJSON = `{"itinerary_name":"Funchal Highlights","overall_description":"A day in Funchal.","points_of_interest":[{"name":"Sé Cathedral","category":"Church","latitude":32.648,"longitude":-16.908},{"name":"Cabo Girão","category":"Viewpoint","latitude":32.656,"longitude":-17.008},{"name":"Monte Cable Car","category":"Attraction","latitude":32.649,"longitude":-16.9}]}`
)

func section(tag, body string) string {
	return "[" + tag + "]\n" + body + "\n\n"
}

func TestFormatResponseForDisplayMultiPart(t *testing.T) {
	const city = "Funchal"

	ordered := section("city_data", testCityDataJSON) +
		section("general_pois", testGeneralPOIsJSON) +
		section("itinerary", testItineraryJSON)
	reversed := section("itinerary", testItineraryJSON) +
		section("general_pois", testGeneralPOIsJSON) +
		section("city_data", testCityDataJSON)

	cases := []struct {
		name     string
		response string
		want     string // exact expected output; empty means only the invariants are checked
		contains string
	}{
		{
			name:     "three sections in city_data, general_pois, itinerary order",
			response: ordered,
			contains: "Funchal Highlights",
		},
		{
			name:     "three sections in reversed order",
			response: reversed,
			contains: "Funchal Highlights",
		},
		{
			name:     "general_pois only",
			response: section("general_pois", testGeneralPOIsJSON),
			contains: "Monte Palace Tropical Garden",
		},
		{
			name:     "legacy single itinerary blob",
			response: "[itinerary]" + testItineraryJSON,
			contains: "Funchal Highlights",
		},
		{
			name:     "plain text passes through unchanged",
			response: "Here are some great places to visit in Funchal!",
			want:     "Here are some great places to visit in Funchal!",
		},
		{
			name:     "empty sections fall back to a generic sentence",
			response: "[city_data]\n\n[general_pois]\n\n",
			want:     "I provided personalized recommendations for Funchal. Here are some great options I found for you!",
		},
		{
			name:     "sections holding empty containers fall back to the keyword sentence",
			response: section("general_pois", `{"points_of_interest":[]}`) + section("itinerary", `{}`),
			want:     "I found some exciting places to visit in Funchal for you!",
		},
	}

	results := make(map[string]string, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatResponseForDisplay(tc.response, city)
			results[tc.name] = got

			if got == "" {
				t.Fatalf("formatResponseForDisplay returned an empty string")
			}
			if strings.HasPrefix(got, "[") || strings.HasPrefix(got, "{") {
				t.Fatalf("formatResponseForDisplay leaked raw payload to the display: %q", got)
			}
			if tc.want != "" && got != tc.want {
				t.Fatalf("formatResponseForDisplay = %q, want %q", got, tc.want)
			}
			if tc.contains != "" && !strings.Contains(got, tc.contains) {
				t.Fatalf("formatResponseForDisplay = %q, want it to mention %q", got, tc.contains)
			}
		})
	}

	// Section order comes from a Go map range at write time, so the same
	// interaction must render identically regardless of how it was laid out.
	if a, b := results[cases[0].name], results[cases[1].name]; a != b {
		t.Fatalf("section order changed the rendered history:\n ordered:  %q\n reversed: %q", a, b)
	}
}

func TestFormatResponseForDisplayPrefersProseOverLeakedJSON(t *testing.T) {
	// A section whose body cannot be turned into prose must not surface the
	// raw JSON of another section either.
	response := section("city_data", `{"foo":"bar"}`) + section("hotels", `[{"name":"Reid's Palace"}]`)
	got := formatResponseForDisplay(response, "Funchal")
	if !strings.Contains(got, "Reid's Palace") {
		t.Fatalf("expected the hotel section to be rendered, got %q", got)
	}
	if strings.HasPrefix(got, "[") || strings.HasPrefix(got, "{") {
		t.Fatalf("raw payload leaked: %q", got)
	}
}

func TestStripPromptWrapper(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "wrapped prompt yields the user's message",
			prompt: "Unified Chat Stream - Domain: itinerary, Message: Itinerary in Funchal.",
			want:   "Itinerary in Funchal.",
		},
		{
			name:   "unwrapped prompt is unchanged",
			prompt: "Itinerary in Funchal.",
			want:   "Itinerary in Funchal.",
		},
		{
			name:   "multi-line message is preserved",
			prompt: "Unified Chat Stream - Domain: general, Message: First line\nSecond line",
			want:   "First line\nSecond line",
		},
		{
			name:   "domain with underscore and surrounding whitespace",
			prompt: "Unified Chat Stream - Domain: general_pois, Message:   Hotels near the marina  ",
			want:   "Hotels near the marina",
		},
		{
			name:   "empty prompt is unchanged",
			prompt: "",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripPromptWrapper(tc.prompt); got != tc.want {
				t.Fatalf("stripPromptWrapper(%q) = %q, want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

func TestFormatResponseForDisplayWrappedHotelsSection(t *testing.T) {
	blob := "[hotels]\n{\"hotels\":[{\"name\":\"Belmond Reid's Palace\",\"category\":\"hotel\"}]}\n\n[city_data]\n{}\n"
	got := formatResponseForDisplay(blob, "Funchal")
	if strings.HasPrefix(got, "[") || strings.HasPrefix(got, "{") {
		t.Fatalf("raw payload leaked: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "hotel") {
		t.Fatalf("expected the hotel formatter, got %q", got)
	}
	if !strings.Contains(got, "Belmond") {
		t.Fatalf("expected the hotel name to be mentioned, got %q", got)
	}
}
