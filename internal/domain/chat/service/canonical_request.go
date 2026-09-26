package service

import (
	"sort"
	"strings"
	"unicode"
)

// canonicalRequestText folds a request into the form the generation key
// hashes, so that requests which ask for the same thing share one cached
// answer: "Food in Madeira", "gastronomy in madeira?" and "Madeira cuisine"
// all become "food"; "3 days in Funchal" and "three days in Funchal" both
// become "3 day".
//
// It is deliberately deterministic and conservative. It lower-cases, drops
// the city's own name (the key already carries the city), drops filler words
// that do not change what is asked for, folds a closed list of synonyms and
// plurals onto one spelling, and sorts what is left, so word order does not
// matter. Anything it does not recognise is kept as-is: an unknown word makes
// two requests differ, which costs a cache miss, never a wrong answer.
//
// Settings are not part of this text. They reach the key through the scoped
// profile snapshot, so "summer activities in Madeira" asked by a traveller
// whose profile prefers winter is still a different entry.
func canonicalRequestText(query, cityName string) string {
	cityWords := make(map[string]struct{})
	for _, w := range requestWords(cityName) {
		cityWords[w] = struct{}{}
	}

	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	for _, w := range requestWords(query) {
		if _, isCity := cityWords[w]; isCity {
			continue
		}
		if _, filler := requestFillerWords[w]; filler {
			continue
		}
		w = canonicalRequestWord(w)
		if w == "" {
			continue
		}
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// requestWords splits text into lower-case words of letters and digits.
// Apostrophes are dropped rather than split on, so "what's" is "whats".
func requestWords(s string) []string {
	s = strings.ToLower(strings.NewReplacer("'", "", "’", "").Replace(s))
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// canonicalRequestWord maps a word onto its canonical spelling: number words
// to digits, then a synonym, then a naive singular.
func canonicalRequestWord(w string) string {
	if n, ok := requestNumberWords[w]; ok {
		return n
	}
	// A synonym may map to "": a word that only names the domain.
	if c, ok := requestSynonyms[w]; ok {
		return c
	}
	if s := singularRequestWord(w); s != w {
		if c, ok := requestSynonyms[s]; ok {
			return c
		}
		return s
	}
	return w
}

// singularRequestWord strips a plural "s" from words long enough for that to
// be safe ("museums" → "museum", but not "bus", "pass" or "gas").
func singularRequestWord(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return strings.TrimSuffix(w, "s")
	}
	return w
}

// requestFillerWords do not change what a request asks for.
var requestFillerWords = toSet(
	"a", "an", "the", "in", "at", "on", "of", "for", "to", "from",
	"and", "or", "with", "about", "into",
	"i", "me", "my", "we", "us", "our", "you", "your",
	"please", "pls", "can", "could", "would", "should", "will",
	"show", "give", "find", "tell", "list", "suggest", "recommend", "recommendation",
	"recommendations", "suggestion", "suggestions", "want", "like", "need", "looking",
	"search", "searching", "get",
	"what", "whats", "which", "where", "wheres", "how", "is", "are", "there", "some", "any",
	"best", "top", "good", "great", "nice", "must", "famous", "popular",
	"city", "town", "visit", "visiting", "travel",
)

// requestSynonyms folds words that ask for the same thing onto one spelling.
// Keep entries to true synonyms: "hostel" is not "hotel" (it is a different
// price), so it is not folded.
var requestSynonyms = map[string]string{
	// Gastronomy
	"food": "food", "foods": "food", "gastronomy": "food", "gastronomic": "food",
	"cuisine": "food", "cuisines": "food", "dish": "food", "dishes": "food",
	"eat": "food", "eating": "food", "specialty": "food", "specialties": "food",
	"speciality": "food", "specialities": "food", "delicacy": "food", "delicacies": "food",
	"foodie": "food", "culinary": "food",
	// Dining
	"restaurant": "restaurant", "restaurants": "restaurant", "eatery": "restaurant",
	"eateries": "restaurant", "dine": "restaurant", "dining": "restaurant",
	"diner": "restaurant", "diners": "restaurant",
	// Accommodation
	"hotel": "hotel", "hotels": "hotel", "accommodation": "hotel", "accommodations": "hotel",
	"lodging": "hotel", "stay": "hotel", "stays": "hotel", "sleep": "hotel",
	// Activities
	"activity": "activity", "activities": "activity", "thing": "activity", "things": "activity",
	"do": "activity", "experience": "activity", "experiences": "activity",
	"attraction": "activity", "attractions": "activity", "sight": "activity",
	"sights": "activity", "sightseeing": "activity",
	// Itinerary words only name the domain, which the key already carries:
	// "3 days in Funchal" and "3-day trip to Funchal" ask for the same plan.
	"itinerary": "", "itineraries": "", "plan": "", "plans": "", "schedule": "",
	"route": "", "journey": "", "holiday": "", "vacation": "", "trip": "", "trips": "",
	// Durations
	"day": "day", "days": "day", "night": "night", "nights": "night",
	"week": "week", "weeks": "week", "weekend": "weekend", "weekends": "weekend",
	// Seasons
	"autumn": "fall",
}

// requestNumberWords turns small number words into digits.
var requestNumberWords = map[string]string{
	"one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6",
	"seven": "7", "eight": "8", "nine": "9", "ten": "10", "eleven": "11",
	"twelve": "12", "fourteen": "14",
}

func toSet(words ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}
