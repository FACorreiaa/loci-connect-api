package locitypes

import (
	"context"
	"strings"
	"testing"
	"unicode"

	a "github.com/petar-dambovaliev/aho-corasick"
)

// Current regex implementation (from chat.go)
func detectDomainRegex(message string) DomainType {
	detector := &DomainDetector{}
	return detector.DetectDomain(context.Background(), message)
}

// Your 4-matcher approach
// revive:disable-next-line:var-naming
var (
	accommBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	accommMatcher = accommBuilder.Build([]string{"hotel", "hotels", "hostel", "hostels", "accommodation", "stay", "sleep", "room", "rooms", "booking", "bookings", "airbnb", "lodge", "resort", "resorts", "guesthouse", "guesthouses"})

	diningBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	diningMatcher = diningBuilder.Build([]string{"restaurant", "restaurants", "food", "eat", "dine", "meal", "meals", "cuisine", "drink", "drinks", "cafe", "cafes", "bar", "bars", "lunch", "dinner", "breakfast", "brunch"})

	activBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	activMatcher = activBuilder.Build([]string{"activity", "activities", "museum", "museums", "park", "parks", "attraction", "attractions", "tour", "tours", "visit", "see", "do", "experience", "experiences", "adventure", "adventures", "shopping", "nightlife"})

	itinBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	itinMatcher = itinBuilder.Build([]string{"itinerary", "itineraries", "plan", "plans", "schedule", "trip", "trips", "day", "week", "journey", "journeys", "route", "routes", "organize", "arrange"})
)

func hasMatch(m a.AhoCorasick, s string) bool {
	iter := m.Iter(s)
	return iter.Next() != nil
}

func fallbackDomain(message string) DomainType {
	bestDomain := DomainGeneral
	bestPriority := 999
	seen := make(map[DomainType]bool)

	for _, token := range strings.FieldsFunc(message, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if token == "" {
			continue
		}
		candidates := []string{token}
		if strings.HasSuffix(token, "s") {
			candidates = append(candidates, strings.TrimSuffix(token, "s"))
		}
		for _, c := range candidates {
			if domain, ok := keywordMap[c]; ok {
				if seen[domain] {
					continue
				}
				seen[domain] = true
				if priority := domainPriority[domain]; priority < bestPriority {
					bestPriority = priority
					bestDomain = domain
				}
			}
		}
	}

	return bestDomain
}

func detectDomainFourMatchers(message string) DomainType {
	message = strings.ToLower(message)

	switch true {
	case hasMatch(accommMatcher, message):
		return DomainAccommodation
	case hasMatch(diningMatcher, message):
		return DomainDining
	case hasMatch(activMatcher, message):
		return DomainActivities
	case hasMatch(itinMatcher, message):
		return DomainItinerary
	default:
		return fallbackDomain(message)
	}
}

// Optimized single-matcher approach
var (
	singleMatcherBuilder = a.NewAhoCorasickBuilder(a.Opts{
		AsciiCaseInsensitive: true,
		MatchOnlyWholeWords:  true,
	})
	singleMatcher = singleMatcherBuilder.Build([]string{
		"hotel", "hotels", "hostel", "hostels", "accommodation", "stay", "sleep", "room", "rooms",
		"booking", "bookings", "airbnb", "lodge", "resort", "resorts", "guesthouse", "guesthouses",
		"restaurant", "restaurants", "food", "eat", "dine", "meal", "meals", "cuisine",
		"drink", "drinks", "cafe", "cafes", "bar", "bars", "lunch", "dinner", "breakfast", "brunch",
		"activity", "activities", "museum", "museums", "park", "parks", "attraction", "attractions", "tour", "tours", "visit",
		"see", "do", "experience", "experiences", "adventure", "adventures", "shopping", "nightlife",
		"itinerary", "itineraries", "plan", "plans", "schedule", "trip", "trips", "day", "week",
		"journey", "journeys", "route", "routes", "organize", "arrange",
	})

	keywordMap = map[string]DomainType{
		"hotel": DomainAccommodation, "hotels": DomainAccommodation, "hostel": DomainAccommodation, "hostels": DomainAccommodation,
		"accommodation": DomainAccommodation, "stay": DomainAccommodation,
		"sleep": DomainAccommodation, "room": DomainAccommodation, "rooms": DomainAccommodation,
		"booking": DomainAccommodation, "bookings": DomainAccommodation, "airbnb": DomainAccommodation,
		"lodge": DomainAccommodation, "resort": DomainAccommodation, "resorts": DomainAccommodation,
		"guesthouse": DomainAccommodation, "guesthouses": DomainAccommodation,
		"restaurant": DomainDining, "restaurants": DomainDining, "food": DomainDining,
		"eat": DomainDining, "dine": DomainDining,
		"meal": DomainDining, "meals": DomainDining, "cuisine": DomainDining,
		"drink": DomainDining, "drinks": DomainDining, "cafe": DomainDining, "cafes": DomainDining,
		"bar": DomainDining, "bars": DomainDining, "lunch": DomainDining,
		"dinner": DomainDining, "breakfast": DomainDining,
		"brunch":   DomainDining,
		"activity": DomainActivities, "activities": DomainActivities, "museum": DomainActivities, "museums": DomainActivities,
		"park": DomainActivities, "parks": DomainActivities, "attraction": DomainActivities, "attractions": DomainActivities,
		"tour": DomainActivities, "tours": DomainActivities, "visit": DomainActivities,
		"see": DomainActivities, "do": DomainActivities,
		"experience": DomainActivities, "experiences": DomainActivities, "adventure": DomainActivities, "adventures": DomainActivities,
		"shopping": DomainActivities, "nightlife": DomainActivities,
		"itinerary": DomainItinerary, "itineraries": DomainItinerary, "plan": DomainItinerary, "plans": DomainItinerary,
		"schedule": DomainItinerary, "trip": DomainItinerary, "trips": DomainItinerary,
		"day": DomainItinerary, "week": DomainItinerary,
		"journey": DomainItinerary, "journeys": DomainItinerary, "route": DomainItinerary, "routes": DomainItinerary,
		"organize": DomainItinerary, "arrange": DomainItinerary,
	}
)

func detectDomainSingleMatcher(message string) DomainType {
	message = strings.ToLower(message)
	matches := singleMatcher.FindAll(message)

	if len(matches) == 0 {
		return fallbackDomain(message)
	}

	// Return first match's domain
	matchedWord := message[matches[0].Start():matches[0].End()]
	return keywordMap[matchedWord]
}

// Benchmark test cases
var testMessages = []string{
	"I need a hotel near the Eiffel Tower",                          // Accommodation (early match)
	"What are some good restaurants in Tokyo?",                      // Dining (early match)
	"I want to visit museums and parks tomorrow",                    // Activities (early match)
	"Help me plan my itinerary for next week",                       // Itinerary (early match)
	"This is a long message about my travel plans and I'm not sure", // General (no match, worst case)
}

func BenchmarkRegex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, msg := range testMessages {
			detectDomainRegex(msg)
		}
	}
}

func BenchmarkFourMatchers(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, msg := range testMessages {
			detectDomainFourMatchers(msg)
		}
	}
}

func BenchmarkSingleMatcher(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, msg := range testMessages {
			detectDomainSingleMatcher(msg)
		}
	}
}

// Test correctness
func TestDomainDetectionCorrectness(t *testing.T) {
	tests := []struct {
		message  string
		expected DomainType
	}{
		{"I need a hotel", DomainAccommodation},
		{"Hotels in Rome", DomainAccommodation},
		{"Where can I eat?", DomainDining},
		{"What museums should I visit?", DomainActivities},
		{"Help me plan my trip", DomainItinerary},
		{"Random message", DomainGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			regexResult := detectDomainRegex(tt.message)
			fourMatcherResult := detectDomainFourMatchers(tt.message)
			singleMatcherResult := detectDomainSingleMatcher(tt.message)

			if regexResult != tt.expected {
				t.Errorf("Regex: got %v, want %v", regexResult, tt.expected)
			}
			if fourMatcherResult != tt.expected {
				t.Errorf("FourMatchers: got %v, want %v", fourMatcherResult, tt.expected)
			}
			if singleMatcherResult != tt.expected {
				t.Errorf("SingleMatcher: got %v, want %v", singleMatcherResult, tt.expected)
			}
		})
	}
}
