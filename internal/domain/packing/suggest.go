// Package packing turns what we know about a trip into a packing list.
//
// The existing checklist lets people type items in by hand, which any notes app
// does. What a notes app cannot do is know that it will rain on two of your five
// days in Lisbon, that you have four hours of driving, and that you said you care
// about hiking — and suggest accordingly.
//
// Like the go-score and the multi-city planner, this is a pure function over
// supplied inputs: no I/O, no clock, no provider calls. Every suggestion carries
// the reason it was made, for the same reason the score does — an unexplained
// suggestion is noise, and the user needs to be able to disagree with it.
package packing

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
)

// Category groups items so a long list stays scannable.
type Category string

const (
	CategoryEssentials Category = "essentials"
	CategoryClothing   Category = "clothing"
	CategoryWeather    Category = "weather"
	CategoryTech       Category = "tech"
	CategoryHealth     Category = "health"
	CategoryTravel     Category = "travel"
	CategoryActivity   Category = "activity"
)

// Item is one suggestion.
type Item struct {
	Text     string   `json:"text"`
	Category Category `json:"category"`
	// Reason is why this trip earned this item. Empty for universal essentials,
	// which need no justification.
	Reason string `json:"reason"`
	// Essential marks things it would be genuinely bad to forget, so a UI can
	// lead with them.
	Essential bool `json:"essential"`
}

// CityWindow is one city and the days spent there, with its forecast.
type CityWindow struct {
	Name string
	Days int
	// Forecast for this city's days. Empty means unknown, which produces no
	// weather suggestions rather than guessed ones.
	Forecast []localcontext.WeatherDay
}

// Input is everything the suggestions are derived from.
type Input struct {
	// TotalDays across the whole trip.
	TotalDays int
	Cities    []CityWindow
	// Interests as the user described them (from trip constraints).
	Interests []string
	// Mobility e.g. "walking", "wheelchair", "transit", "car".
	Mobility string
	// BudgetLevel 1-4, where 1 is frugal.
	BudgetLevel int
	// TotalDriveMins across every inter-city leg.
	TotalDriveMins int
	// TravelDays counts days that include a move between cities.
	TravelDays int
}

// Suggest builds the list. Order is deliberate: essentials first, then the
// weather-driven items (the ones people actually forget), then the rest.
func Suggest(in Input) []Item {
	var items []Item

	items = append(items, essentials(in)...)
	items = append(items, weatherItems(in)...)
	items = append(items, clothingItems(in)...)
	items = append(items, travelItems(in)...)
	items = append(items, activityItems(in)...)

	return dedupe(items)
}

// essentials are the things it would be bad to forget on any trip. They carry no
// reason: "why do I need my passport" is not a question worth answering.
func essentials(in Input) []Item {
	items := []Item{
		{Text: "ID or passport", Category: CategoryEssentials, Essential: true},
		{Text: "Payment cards", Category: CategoryEssentials, Essential: true},
		{Text: "Phone charger", Category: CategoryTech, Essential: true},
		{Text: "Any regular medication", Category: CategoryHealth, Essential: true},
	}

	if in.TotalDays >= 4 {
		items = append(items, Item{
			Text:     "Toiletries in full size, not travel minis",
			Category: CategoryEssentials,
			Reason:   fmt.Sprintf("%d days is long enough to run out", in.TotalDays),
		})
	}
	return items
}

// weatherItems are the highest-value suggestions, because they are specific to
// this trip and this week, and they are what people forget.
func weatherItems(in Input) []Item {
	var items []Item

	for _, city := range in.Cities {
		if len(city.Forecast) == 0 {
			continue
		}

		var wet, cold, hot int
		var maxPrecip float64
		for _, d := range city.Forecast {
			if isWet(d) {
				wet++
			}
			if d.LowC < 10 {
				cold++
			}
			if d.HighC > 28 {
				hot++
			}
			if d.PrecipProb > maxPrecip {
				maxPrecip = d.PrecipProb
			}
		}

		if wet > 0 {
			items = append(items,
				Item{
					Text:     "Rain jacket or compact umbrella",
					Category: CategoryWeather,
					Reason:   fmt.Sprintf("rain likely on %s in %s", dayCount(wet, len(city.Forecast)), city.Name),
				},
				Item{
					Text:     "Shoes you do not mind getting wet",
					Category: CategoryClothing,
					Reason:   fmt.Sprintf("wet days forecast in %s", city.Name),
				})
		}
		if cold > 0 {
			items = append(items, Item{
				Text:     "Warm layer for the evenings",
				Category: CategoryClothing,
				Reason:   fmt.Sprintf("nights below 10°C in %s", city.Name),
			})
		}
		if hot > 0 {
			items = append(items,
				Item{
					Text:     "Sun protection",
					Category: CategoryWeather,
					Reason:   fmt.Sprintf("highs above 28°C in %s", city.Name),
				},
				Item{
					Text:     "Refillable water bottle",
					Category: CategoryHealth,
					Reason:   fmt.Sprintf("hot days forecast in %s", city.Name),
				})
		}
	}
	return items
}

func clothingItems(in Input) []Item {
	var items []Item

	// Walking is the default way to see a city, so this is nearly always right,
	// but say why rather than asserting it.
	if in.Mobility == "" || strings.EqualFold(in.Mobility, "walking") {
		items = append(items, Item{
			Text:     "Shoes you can walk all day in",
			Category: CategoryClothing,
			Reason:   "city days are mostly on foot",
		})
	}

	if in.TotalDays >= 5 {
		items = append(items, Item{
			Text:     "Laundry bag",
			Category: CategoryClothing,
			Reason:   fmt.Sprintf("%d days of clothes to keep separate", in.TotalDays),
		})
	}

	// Only suggest smart clothes when the budget suggests the kind of place that
	// asks for them.
	if in.BudgetLevel >= 3 && hasInterest(in.Interests, "food", "dining", "restaurant", "fine dining") {
		items = append(items, Item{
			Text:     "One smarter outfit",
			Category: CategoryClothing,
			Reason:   "higher-end dining often has a dress code",
		})
	}
	return items
}

func travelItems(in Input) []Item {
	var items []Item

	if hours := in.TotalDriveMins / 60; hours >= 3 {
		items = append(items,
			Item{
				Text:     "Phone mount and car charger",
				Category: CategoryTech,
				Reason:   fmt.Sprintf("about %dh of driving on this trip", hours),
			},
			Item{
				Text:     "Snacks and water for the road",
				Category: CategoryTravel,
				Reason:   fmt.Sprintf("about %dh of driving on this trip", hours),
			})
	}

	if len(in.Cities) > 1 {
		items = append(items, Item{
			Text:     "Keep documents and chargers in one accessible bag",
			Category: CategoryTravel,
			Reason:   fmt.Sprintf("%d cities means packing and unpacking repeatedly", len(in.Cities)),
		})
	}

	if in.TravelDays > 0 {
		items = append(items, Item{
			Text:     "Something to read or watch offline",
			Category: CategoryTravel,
			Reason:   fmt.Sprintf("%s spent moving between cities", dayLabel(in.TravelDays)),
		})
	}

	if in.BudgetLevel == 1 {
		items = append(items, Item{
			Text:     "Reusable bag for market food",
			Category: CategoryTravel,
			Reason:   "eating cheaply usually means buying from markets",
		})
	}
	return items
}

func activityItems(in Input) []Item {
	var items []Item

	// Each rule maps an interest to the thing that interest actually requires.
	rules := []struct {
		keywords []string
		item     Item
	}{
		{[]string{"beach", "swim", "coast"}, Item{
			Text: "Swimwear and a quick-dry towel", Category: CategoryActivity,
			Reason: "you listed beaches or swimming",
		}},
		{[]string{"hike", "hiking", "trail", "nature", "walking tour"}, Item{
			Text: "Proper walking or hiking shoes", Category: CategoryActivity,
			Reason: "you listed hiking or trails",
		}},
		{[]string{"photo", "photography"}, Item{
			Text: "Spare camera battery and memory card", Category: CategoryTech,
			Reason: "you listed photography",
		}},
		{[]string{"museum", "gallery", "art"}, Item{
			Text: "Small daypack that meets museum bag limits", Category: CategoryActivity,
			Reason: "you listed museums or galleries",
		}},
		{[]string{"nightlife", "bar", "club"}, Item{
			Text: "Something to wear out in the evening", Category: CategoryClothing,
			Reason: "you listed nightlife",
		}},
		{[]string{"cycle", "cycling", "bike"}, Item{
			Text: "Padded shorts or gloves", Category: CategoryActivity,
			Reason: "you listed cycling",
		}},
	}

	for _, rule := range rules {
		if hasInterest(in.Interests, rule.keywords...) {
			items = append(items, rule.item)
		}
	}
	return items
}

func hasInterest(interests []string, keywords ...string) bool {
	for _, i := range interests {
		low := strings.ToLower(i)
		for _, k := range keywords {
			if strings.Contains(low, k) {
				return true
			}
		}
	}
	return false
}

func isWet(d localcontext.WeatherDay) bool {
	c := strings.ToLower(d.Condition)
	if strings.Contains(c, "rain") || strings.Contains(c, "storm") || strings.Contains(c, "snow") {
		return true
	}
	return d.PrecipProb >= 0.5
}

func dayCount(n, of int) string {
	if n == of {
		return fmt.Sprintf("all %s", dayLabel(of))
	}
	return fmt.Sprintf("%d of %d days", n, of)
}

func dayLabel(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

// dedupe keeps the first occurrence of each item text but merges the reasons, so
// "rain jacket" suggested for two cities explains both rather than naming one and
// silently dropping the other.
func dedupe(items []Item) []Item {
	byText := map[string]*Item{}
	var order []string

	for i := range items {
		key := strings.ToLower(items[i].Text)
		existing, seen := byText[key]
		if !seen {
			copied := items[i]
			byText[key] = &copied
			order = append(order, key)
			continue
		}
		if items[i].Reason != "" && existing.Reason != items[i].Reason {
			existing.Reason = mergeReasons(existing.Reason, items[i].Reason)
		}
		existing.Essential = existing.Essential || items[i].Essential
	}

	out := make([]Item, 0, len(order))
	for _, key := range order {
		out = append(out, *byText[key])
	}

	// Essentials first; otherwise keep insertion order so the grouping above
	// still reads sensibly.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Essential && !out[j].Essential
	})
	return out
}

func mergeReasons(a, b string) string {
	if strings.Contains(a, b) {
		return a
	}
	return a + "; " + b
}
