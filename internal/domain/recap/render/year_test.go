package render

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
)

func yearRecap() *recapv1.Recap {
	months := make([]*recapv1.TimelineMonth, 12)
	for i := range months {
		months[i] = &recapv1.TimelineMonth{Month: int32(i + 1)}
	}
	months[2].TripsCount, months[2].Days = 1, 4
	months[5].TripsCount, months[5].Days = 2, 9
	months[9].TripsCount, months[9].Days = 1, 5
	return &recapv1.Recap{
		Kind:        recapv1.RecapKind_RECAP_KIND_YEAR,
		Year:        2026,
		Title:       "Your 2026",
		PeriodLabel: "2026",
		Cards: []*recapv1.RecapCard{
			{Id: "year_map", Card: &recapv1.RecapCard_YearMap{YearMap: &recapv1.YearMapCard{
				CountriesCount: 3, CitiesCount: 6, NewCountriesCount: 1,
				Countries: []*recapv1.YearCountry{
					{Country: "Portugal", CountryCode: "PT", CitiesCount: 3, Days: 11},
					{Country: "Spain", CountryCode: "ES", CitiesCount: 2, Days: 5},
					{Country: "Morocco", CountryCode: "MA", CitiesCount: 1, Days: 2, FirstTime: true},
				},
				Cities: []*recapv1.RoutePoint{
					{Latitude: 38.72, Longitude: -9.14, Label: "Lisbon"},
					{Latitude: 41.15, Longitude: -8.61, Label: "Porto"},
					{Latitude: 37.02, Longitude: -7.93, Label: "Faro"},
					{Latitude: 40.42, Longitude: -3.70, Label: "Madrid"},
					{Latitude: 41.39, Longitude: 2.17, Label: "Barcelona"},
					{Latitude: 31.63, Longitude: -8.01, Label: "Marrakesh"},
				},
				Headline: "Six cities in three countries, one of them new to you.",
			}}},
			{Id: "trips_timeline", Card: &recapv1.RecapCard_TripsTimeline{TripsTimeline: &recapv1.TripsTimelineCard{
				TripsCount: 4, DaysAway: 18, Months: months, BusiestMonth: 6,
				Trips: []*recapv1.TimelineTrip{
					{Month: 3, CityName: "Porto", DayCount: 4},
					{Month: 6, CityName: "Madrid", DayCount: 5},
					{Month: 6, CityName: "Marrakesh", DayCount: 4},
					{Month: 10, CityName: "Lisbon", DayCount: 5},
				},
				Headline: "4 trips, 18 days away. June was the busiest.",
			}}},
			{Id: "top_city", Card: &recapv1.RecapCard_TopCity{TopCity: &recapv1.TopCityCard{
				CityName: "Lisbon", Country: "Portugal", CountryCode: "PT", Days: 9, PlacesCount: 41, TripsCount: 2,
				Headline: "Lisbon was your city this year: 9 days and 41 places.",
			}}},
			{Id: "taste_shift", Card: &recapv1.RecapCard_TasteShift{TasteShift: &recapv1.TasteShiftCard{
				Trait:         &recapv1.TasteTrait{Label: "Natural wine bars", Strength: 1, Trend: recapv1.TasteTrend_TASTE_TREND_STRONG},
				StrengthStart: 0.22, StrengthEnd: 1,
				RunnersUp: []*recapv1.TasteTrait{{Label: "Tiled older streets"}, {Label: "Bookshops"}},
				Headline:  "2026 was the year you got into natural wine bars.",
			}}},
			{Id: "streak", Card: &recapv1.RecapCard_Streak{Streak: &recapv1.StreakCard{
				LongestStreakDays: 9, ActiveDays: 64, BestMonth: 6,
				Headline: "9 days in a row in June, and 64 active days in all.",
			}}},
			{Id: "contributions", Card: &recapv1.RecapCard_Contributions{Contributions: &recapv1.ContributionsCard{
				ReviewsCount: 12, ListsCount: 3, PostsCount: 0, SubmissionsCount: 2,
				Headline: "You gave back: 12 reviews, 3 lists and 2 places added.",
			}}},
			{Id: "percentile", Card: &recapv1.RecapCard_Percentile{Percentile: &recapv1.PercentileCard{
				TopPercent: 5, Metric: recapv1.PercentileMetric_PERCENTILE_METRIC_PLACES, Value: 87,
				Headline: "Top 5% of Loci travellers for places kept.",
			}}},
		},
	}
}

func TestEveryYearCardRenders(t *testing.T) {
	r := yearRecap()
	for _, c := range r.GetCards() {
		t.Run(c.GetId(), func(t *testing.T) {
			t.Parallel()
			b, err := Card(r, c)
			if err != nil {
				t.Fatal(err)
			}
			decode(t, b, StoryW, StoryH)
		})
	}
}

func TestYearCardsStress(t *testing.T) {
	long := strings.Repeat("Llanfairpwllgwyngyllgogerychwyrndrobwllllantysiliogogogoch ", 6)
	pts := make([]*recapv1.RoutePoint, 60)
	for i := range pts {
		pts[i] = &recapv1.RoutePoint{Latitude: float64(i) - 30, Longitude: float64(i*6) - 180, Label: long}
	}
	pts[2].Longitude = math.Inf(1)
	countries := make([]*recapv1.YearCountry, 20)
	for i := range countries {
		countries[i] = &recapv1.YearCountry{Country: long, CitiesCount: 1 << 30, Days: -3, FirstTime: i%2 == 0}
	}
	trips := make([]*recapv1.TimelineTrip, 40)
	for i := range trips {
		trips[i] = &recapv1.TimelineTrip{Month: int32(i % 14), CityName: long, DayCount: 1 << 30}
	}
	months := []*recapv1.TimelineMonth{{Month: 0, Days: 5}, {Month: 13, Days: 9}, {Month: 4, Days: math.MaxInt32}, {Month: 5, Days: -8}}
	cards := []*recapv1.RecapCard{
		{Card: &recapv1.RecapCard_YearMap{YearMap: &recapv1.YearMapCard{CountriesCount: math.MaxInt32, CitiesCount: -1, Countries: countries, Cities: pts, Headline: long}}},
		{Card: &recapv1.RecapCard_YearMap{YearMap: &recapv1.YearMapCard{}}},
		{Card: &recapv1.RecapCard_TripsTimeline{TripsTimeline: &recapv1.TripsTimelineCard{TripsCount: math.MaxInt32, Months: months, Trips: trips, BusiestMonth: 4, Headline: long}}},
		{Card: &recapv1.RecapCard_TripsTimeline{TripsTimeline: &recapv1.TripsTimelineCard{}}},
		{Card: &recapv1.RecapCard_TopCity{TopCity: &recapv1.TopCityCard{CityName: long, Country: long, Days: -1, PlacesCount: math.MaxInt32, Headline: long}}},
		{Card: &recapv1.RecapCard_TopCity{TopCity: &recapv1.TopCityCard{}}},
		{Card: &recapv1.RecapCard_TasteShift{TasteShift: &recapv1.TasteShiftCard{Trait: &recapv1.TasteTrait{Label: long}, StrengthStart: math.NaN(), StrengthEnd: 9, RunnersUp: []*recapv1.TasteTrait{{Label: long}, {}, {Label: "dropped"}}, Headline: long}}},
		{Card: &recapv1.RecapCard_TasteShift{TasteShift: &recapv1.TasteShiftCard{}}},
		{Card: &recapv1.RecapCard_Streak{Streak: &recapv1.StreakCard{LongestStreakDays: math.MaxInt32, ActiveDays: -5, BestMonth: 99, Headline: long}}},
		{Card: &recapv1.RecapCard_Streak{Streak: &recapv1.StreakCard{}}},
		{Card: &recapv1.RecapCard_Contributions{Contributions: &recapv1.ContributionsCard{ReviewsCount: math.MaxInt32, ListsCount: math.MaxInt32, PostsCount: -1, Headline: long}}},
		{Card: &recapv1.RecapCard_Contributions{Contributions: &recapv1.ContributionsCard{}}},
		{Card: &recapv1.RecapCard_Percentile{Percentile: &recapv1.PercentileCard{TopPercent: -40, Metric: 77, Value: math.MaxInt32, Headline: long}}},
		{Card: &recapv1.RecapCard_Percentile{Percentile: &recapv1.PercentileCard{}}},
	}
	r := &recapv1.Recap{Title: long, PeriodLabel: long, Theme: &recapv1.RecapTheme{AccentHex: "#zzzzzz"}, Cards: cards}
	for i, c := range cards {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			t.Parallel()
			for _, rr := range []*recapv1.Recap{r, nil} {
				b, err := Card(rr, c)
				if err != nil {
					t.Fatal(err)
				}
				decode(t, b, StoryW, StoryH)
			}
		})
	}
	b, err := OG(r)
	if err != nil {
		t.Fatal(err)
	}
	decode(t, b, OGW, OGH)
}

// TestWriteYearSamples writes the year cards for eyeballing; it only runs
// when RECAP_SAMPLES_DIR is set.
func TestWriteYearSamples(t *testing.T) {
	dir := os.Getenv("RECAP_SAMPLES_DIR")
	if dir == "" {
		t.Skip("set RECAP_SAMPLES_DIR to write sample PNGs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := yearRecap()
	for i, c := range r.GetCards() {
		b, err := Card(r, c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("year-%02d-%s.png", i+1, c.GetId())), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
