package packing

import (
	"strings"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/localcontext"
)

func forecast(n int, condition string, precip, high, low float64) []localcontext.WeatherDay {
	days := make([]localcontext.WeatherDay, n)
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	for i := range days {
		days[i] = localcontext.WeatherDay{
			Date: base.AddDate(0, 0, i), Condition: condition,
			PrecipProb: precip, HighC: high, LowC: low,
		}
	}
	return days
}

func texts(items []Item) string {
	var b strings.Builder
	for _, i := range items {
		b.WriteString(i.Text)
		b.WriteString(" | ")
		b.WriteString(i.Reason)
		b.WriteString("\n")
	}
	return b.String()
}

func find(items []Item, substr string) (Item, bool) {
	for _, i := range items {
		if strings.Contains(strings.ToLower(i.Text), strings.ToLower(substr)) {
			return i, true
		}
	}
	return Item{}, false
}

// Every trip gets the things it would be genuinely bad to forget, flagged so the
// UI can lead with them.
func TestSuggest_AlwaysIncludesEssentials(t *testing.T) {
	got := Suggest(Input{TotalDays: 2, Cities: []CityWindow{{Name: "Lisbon", Days: 2}}})

	for _, want := range []string{"passport", "cards", "charger", "medication"} {
		item, ok := find(got, want)
		if !ok {
			t.Errorf("missing essential %q in:\n%s", want, texts(got))
			continue
		}
		if !item.Essential {
			t.Errorf("%q should be flagged essential", item.Text)
		}
	}
}

// The point of the feature: suggestions specific to this trip's actual forecast.
func TestSuggest_RainProducesRainGearWithTheReason(t *testing.T) {
	got := Suggest(Input{
		TotalDays: 3,
		Cities: []CityWindow{
			{Name: "Lisbon", Days: 3, Forecast: forecast(3, "Rain", 0.8, 18, 14)},
		},
	})

	item, ok := find(got, "umbrella")
	if !ok {
		t.Fatalf("expected rain gear:\n%s", texts(got))
	}
	if !strings.Contains(item.Reason, "Lisbon") {
		t.Errorf("the reason should name the city, got %q", item.Reason)
	}
	if !strings.Contains(item.Reason, "rain") {
		t.Errorf("the reason should mention rain, got %q", item.Reason)
	}
}

// A forecast we do not have must not produce guessed weather advice. Suggesting a
// raincoat for weather we never checked is worse than saying nothing.
func TestSuggest_NoForecastMeansNoWeatherAdvice(t *testing.T) {
	got := Suggest(Input{TotalDays: 3, Cities: []CityWindow{{Name: "Lisbon", Days: 3}}})

	for _, unwanted := range []string{"umbrella", "sun protection", "warm layer"} {
		if _, ok := find(got, unwanted); ok {
			t.Errorf("suggested %q with no forecast to justify it:\n%s", unwanted, texts(got))
		}
	}
}

func TestSuggest_ColdNightsAndHotDaysAreHandledSeparately(t *testing.T) {
	cold := Suggest(Input{TotalDays: 2, Cities: []CityWindow{
		{Name: "Porto", Days: 2, Forecast: forecast(2, "Clear", 0.1, 12, 4)},
	}})
	if _, ok := find(cold, "warm layer"); !ok {
		t.Errorf("cold nights should suggest a warm layer:\n%s", texts(cold))
	}
	if _, ok := find(cold, "sun protection"); ok {
		t.Error("cold trip should not suggest sun protection")
	}

	hot := Suggest(Input{TotalDays: 2, Cities: []CityWindow{
		{Name: "Seville", Days: 2, Forecast: forecast(2, "Clear", 0.0, 34, 22)},
	}})
	if _, ok := find(hot, "sun protection"); !ok {
		t.Errorf("hot days should suggest sun protection:\n%s", texts(hot))
	}
	if _, ok := find(hot, "water bottle"); !ok {
		t.Errorf("hot days should suggest water:\n%s", texts(hot))
	}
}

// Interests are the user's own words, so matching has to be forgiving.
func TestSuggest_InterestsDriveActivityItems(t *testing.T) {
	cases := []struct {
		interest string
		want     string
	}{
		{"Beaches", "swimwear"},
		{"hiking trails", "hiking shoes"},
		{"Photography", "camera battery"},
		{"museums and galleries", "daypack"},
		{"cycling", "padded shorts"},
	}

	for _, tc := range cases {
		t.Run(tc.interest, func(t *testing.T) {
			got := Suggest(Input{
				TotalDays: 3,
				Cities:    []CityWindow{{Name: "Lisbon", Days: 3}},
				Interests: []string{tc.interest},
			})
			if _, ok := find(got, tc.want); !ok {
				t.Errorf("interest %q should suggest %q:\n%s", tc.interest, tc.want, texts(got))
			}
		})
	}
}

// A long drive changes what you pack.
func TestSuggest_LongDriveAddsRoadItems(t *testing.T) {
	short := Suggest(Input{TotalDays: 2, Cities: []CityWindow{{Name: "Lisbon", Days: 2}}, TotalDriveMins: 40})
	if _, ok := find(short, "car charger"); ok {
		t.Error("a 40-minute drive should not trigger road items")
	}

	long := Suggest(Input{TotalDays: 4, Cities: []CityWindow{{Name: "Lisbon", Days: 4}}, TotalDriveMins: 260})
	item, ok := find(long, "car charger")
	if !ok {
		t.Fatalf("a 4-hour drive should suggest road items:\n%s", texts(long))
	}
	if !strings.Contains(item.Reason, "driving") {
		t.Errorf("the reason should mention driving, got %q", item.Reason)
	}
}

// Multi-city trips have their own logistics.
func TestSuggest_MultiCityAddsPackingLogistics(t *testing.T) {
	got := Suggest(Input{
		TotalDays: 6,
		Cities: []CityWindow{
			{Name: "Lisbon", Days: 3},
			{Name: "Porto", Days: 3},
		},
		TravelDays: 2,
	})

	if _, ok := find(got, "accessible bag"); !ok {
		t.Errorf("multi-city should mention keeping documents accessible:\n%s", texts(got))
	}
	if _, ok := find(got, "read or watch offline"); !ok {
		t.Errorf("travel days should suggest something for the journey:\n%s", texts(got))
	}
}

// The same item suggested for two cities should appear once, explaining both,
// rather than once naming one city and silently dropping the other.
func TestSuggest_DeduplicatesAndMergesReasons(t *testing.T) {
	got := Suggest(Input{
		TotalDays: 6,
		Cities: []CityWindow{
			{Name: "Lisbon", Days: 3, Forecast: forecast(3, "Rain", 0.9, 18, 13)},
			{Name: "Porto", Days: 3, Forecast: forecast(3, "Rain", 0.9, 16, 12)},
		},
	})

	count := 0
	var merged Item
	for _, i := range got {
		if strings.Contains(strings.ToLower(i.Text), "umbrella") {
			count++
			merged = i
		}
	}
	if count != 1 {
		t.Fatalf("expected one rain-gear item, got %d:\n%s", count, texts(got))
	}
	if !strings.Contains(merged.Reason, "Lisbon") || !strings.Contains(merged.Reason, "Porto") {
		t.Errorf("the merged reason should name both cities, got %q", merged.Reason)
	}
}

// Dress-code advice is only right when the trip suggests that kind of place.
func TestSuggest_SmartOutfitOnlyForHigherBudgetDining(t *testing.T) {
	frugal := Suggest(Input{
		TotalDays: 3, Cities: []CityWindow{{Name: "Lisbon", Days: 3}},
		Interests: []string{"food"}, BudgetLevel: 1,
	})
	if _, ok := find(frugal, "smarter outfit"); ok {
		t.Error("a frugal food trip should not demand a dress code outfit")
	}

	fancy := Suggest(Input{
		TotalDays: 3, Cities: []CityWindow{{Name: "Lisbon", Days: 3}},
		Interests: []string{"fine dining"}, BudgetLevel: 4,
	})
	if _, ok := find(fancy, "smarter outfit"); !ok {
		t.Errorf("higher-end dining should suggest smarter clothes:\n%s", texts(fancy))
	}
}

// Essentials should lead, so a user skimming the top of the list sees the things
// that actually matter.
func TestSuggest_EssentialsSortFirst(t *testing.T) {
	got := Suggest(Input{
		TotalDays: 5,
		Cities:    []CityWindow{{Name: "Lisbon", Days: 5, Forecast: forecast(5, "Rain", 0.9, 18, 13)}},
		Interests: []string{"hiking"},
	})

	seenNonEssential := false
	for _, i := range got {
		if !i.Essential {
			seenNonEssential = true
			continue
		}
		if seenNonEssential {
			t.Fatalf("essential %q appears after non-essential items:\n%s", i.Text, texts(got))
		}
	}
}

// Every suggestion beyond the universal essentials must justify itself.
func TestSuggest_NonEssentialsCarryAReason(t *testing.T) {
	got := Suggest(Input{
		TotalDays: 6,
		Cities: []CityWindow{
			{Name: "Lisbon", Days: 3, Forecast: forecast(3, "Rain", 0.8, 20, 14)},
			{Name: "Porto", Days: 3, Forecast: forecast(3, "Clear", 0.1, 30, 20)},
		},
		Interests: []string{"beach", "photography"}, TotalDriveMins: 200, TravelDays: 1,
	})

	for _, i := range got {
		if !i.Essential && i.Reason == "" {
			t.Errorf("suggestion %q has no reason; an unexplained suggestion is noise", i.Text)
		}
	}
}
