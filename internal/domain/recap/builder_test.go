package recap

import (
	"testing"
	"time"

	recapv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recap"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lisbon is a five-day trip with something for every card.
func lisbon() *TripFacts {
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	place := func(id, name, hood, cat string, lat, lon float64, when time.Time) Place {
		return Place{POIID: id, Name: name, Neighborhood: hood, Category: cat, CityName: "Lisbon", Lat: lat, Lon: lon, HasCoords: true, At: when}
	}
	return &TripFacts{
		TripID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		UserID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Title:       "Untitled Trip",
		Timezone:    "Europe/Lisbon",
		PeriodStart: day("2026-10-11"),
		PeriodEnd:   day("2026-10-15"),
		Cities:      []City{{Name: "Lisbon", Country: "Portugal", CountryCode: "PT", Lat: 38.72, Lon: -9.14, HasCoords: true}},
		Days: []Day{
			{Number: 1, Date: ptr(at(11, 9)), CityName: "Lisbon", StopsTotal: 4, StopsDone: 4, Completed: true},
			{Number: 2, Date: ptr(at(12, 9)), CityName: "Lisbon", StopsTotal: 5, StopsDone: 5, Completed: true},
			{Number: 3, Date: ptr(at(13, 9)), CityName: "Lisbon", StopsTotal: 4, StopsDone: 2},
		},
		Places: []Place{
			place("p1", "Miradouro", "Alfama", "viewpoint", 38.711, -9.13, at(11, 10)),
			place("p2", "Tasca", "Alfama", "restaurant", 38.712, -9.131, at(11, 13)),
			place("p3", "Café", "Baixa", "cafe", 38.71, -9.138, at(12, 9)),
			place("p4", "Jerónimos", "Belém", "museum", 38.697, -9.206, at(13, 11)),
		},
		StopsPlanned: 13, StopsDone: 11,
		TripTaste: []Trait{
			{Key: "wine", Label: "Natural wine bars", Weight: 7, Evidence: 7, Saves: 7, Score: 0.8, TotalEvidence: 9},
			{Key: "tiles", Label: "Tiled, older streets", Weight: 5, Evidence: 5, Visits: 5, Score: 0.4, TotalEvidence: 5},
			{Key: "ugly", Label: "Chain coffee", Weight: -2, Evidence: 2},
		},
		Points:      120,
		ActiveDates: []time.Time{day("2026-10-11"), day("2026-10-12"), day("2026-10-14")},
		Badges:      []string{"first-city"},
		Field:       &Field{CityName: "Lisbon", Score: 140, PlacesKept: 4, DaysFinished: 2},
		Friends:     []Friend{{ID: uuid.New(), DisplayName: "Ana"}, {ID: uuid.New(), DisplayName: "Rui"}},
		Highlights:  []HighlightFact{{POIID: "p2", Name: "Tasca", Category: "restaurant", Rating: 5, Reason: HighlightReviewed}},
		Owner:       Owner{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), DisplayName: "Fernando Correia"},
	}
}

func cardIDs(r *recapv1.Recap) []string {
	ids := make([]string, 0, len(r.GetCards()))
	for _, c := range r.GetCards() {
		ids = append(ids, c.GetId())
	}
	return ids
}

func card(r *recapv1.Recap, id string) *recapv1.RecapCard {
	for _, c := range r.GetCards() {
		if c.GetId() == id {
			return c
		}
	}
	return nil
}

func TestBuildTripFull(t *testing.T) {
	rec, reason, ok := NewBuilder().BuildTrip(lisbon())
	require.True(t, ok, reason)

	assert.Equal(t, []string{CardIntro, CardPlaces, CardRoute, CardDays, CardTaste, CardHighlights, CardProgress, CardField, CardFriends, CardOutro}, cardIDs(rec))
	assert.Equal(t, "Lisbon", rec.GetTitle(), "an untitled trip is named after its city")
	assert.Equal(t, "5 days · Oct 2026", rec.GetSubtitle())
	assert.Equal(t, "2026-10-11", rec.GetPeriodStart())
	assert.Equal(t, int32(BuilderVersion), rec.GetBuilderVersion())

	intro := card(rec, CardIntro).GetIntro()
	assert.Equal(t, "LIS", intro.GetShortCode())
	assert.Equal(t, int32(5), intro.GetDayCount())
	assert.Equal(t, "Five days in Lisbon, and the shoes to prove it.", intro.GetLede())

	places := card(rec, CardPlaces).GetPlaces()
	assert.Equal(t, int32(4), places.GetPlacesCount())
	require.Len(t, places.GetGroups(), 3)
	assert.Equal(t, "Alfama", places.GetGroups()[0].GetLabel(), "largest neighbourhood first")
	assert.Equal(t, int32(2), places.GetGroups()[0].GetPlacesCount())
	assert.Equal(t, "2026-10-11", places.GetGroups()[0].GetFirstDate())
	assert.Equal(t, "Kept across three neighbourhoods. Alfama took the most stamps.", places.GetHeadline())

	route := card(rec, CardRoute).GetRoute()
	assert.Equal(t, recapv1.RouteBasis_ROUTE_BASIS_VISITS, route.GetBasis(), "one city and no legs: measured between visits")
	assert.Greater(t, route.GetDistanceKm(), 5.0)
	assert.Equal(t, "Miradouro", route.GetPoints()[0].GetLabel(), "in visit order")

	days := card(rec, CardDays).GetDays()
	assert.Equal(t, int32(2), days.GetDaysCompleted())
	assert.Equal(t, int32(2), days.GetBusiestDayNumber())
	assert.Equal(t, "2 of 3 days walked to the end.", days.GetHeadline())

	taste := card(rec, CardTaste).GetTaste()
	assert.True(t, taste.GetFromPeriod())
	require.Len(t, taste.GetTraits(), 2, "negative evidence is not a taste")
	assert.Equal(t, "You kept going back to natural wine bars.", taste.GetHeadline())
	assert.InDelta(t, 1.0, taste.GetTraits()[0].GetStrength(), 0.001)
	assert.InDelta(t, 0.71, taste.GetTraits()[1].GetStrength(), 0.01)
	assert.Equal(t, recapv1.TasteEvidenceKind_TASTE_EVIDENCE_KIND_SAVES, taste.GetTraits()[0].GetEvidenceKind())
	assert.Equal(t, recapv1.TasteTrend_TASTE_TREND_STRONG, taste.GetTraits()[0].GetTrend())
	assert.Equal(t, recapv1.TasteTrend_TASTE_TREND_NEW, taste.GetTraits()[1].GetTrend(), "all its evidence came from this trip")

	hl := card(rec, CardHighlights).GetHighlights().GetHighlights()
	require.Len(t, hl, 3)
	assert.Equal(t, "Tasca", hl[0].GetName(), "reviewed places first")
	assert.Equal(t, recapv1.HighlightReason_HIGHLIGHT_REASON_FIRST_VISIT, hl[1].GetReason())

	progress := card(rec, CardProgress).GetProgress()
	assert.Equal(t, int64(120), progress.GetPoints())
	assert.Equal(t, int32(2), progress.GetStreakDays())
	assert.Equal(t, "First city", progress.GetBadges()[0].GetLabel())

	field := card(rec, CardField).GetField()
	assert.Equal(t, "Guide", field.GetRankLabel(), "140 in a city is Guide under the defaults")
	assert.Equal(t, "Guide of Lisbon.", field.GetHeadline())

	friends := card(rec, CardFriends).GetFriends()
	assert.Equal(t, "Two of your friends have Lisbon on a trip too.", friends.GetHeadline())

	outro := card(rec, CardOutro).GetOutro()
	assert.Contains(t, outro.GetSummaryLine(), "4 places · ")
	assert.Contains(t, outro.GetSummaryLine(), " km · 5 days", "days as the subtitle counts them")
}

func TestBuildTripThinData(t *testing.T) {
	f := &TripFacts{
		Title:       "Weekend",
		PeriodStart: day("2026-10-11"), PeriodEnd: day("2026-10-12"),
		Cities:       []City{{Name: "Porto"}},
		Days:         []Day{{Number: 1, StopsTotal: 3, StopsDone: 2}},
		StopsPlanned: 3, StopsDone: 2,
	}
	rec, reason, ok := NewBuilder().BuildTrip(f)
	require.True(t, ok, reason)
	assert.Equal(t, []string{CardIntro, CardPlaces, CardDays, CardOutro}, cardIDs(rec))
	assert.Equal(t, "2 of 3 stops on your plan, done.", card(rec, CardPlaces).GetPlaces().GetHeadline())
	assert.Equal(t, "Weekend", rec.GetTitle())
	assert.Equal(t, "POR", card(rec, CardIntro).GetIntro().GetShortCode())
}

func TestBuildTripIneligible(t *testing.T) {
	_, reason, ok := NewBuilder().BuildTrip(&TripFacts{
		PeriodStart: day("2026-10-11"), PeriodEnd: day("2026-10-12"),
		Cities: []City{{Name: "Porto"}}, Days: []Day{{Number: 1, StopsTotal: 3}},
		OverallTaste: []Trait{{Key: "x", Label: "X", Score: 0.9, Confidence: 0.9}},
		Points:       50,
	})
	assert.False(t, ok, "points and taste alone, with nothing visited or walked, are not a trip")
	assert.Equal(t, ReasonNoActivity, reason)

	_, _, ok = NewBuilder().BuildTrip(nil)
	assert.False(t, ok)
}

func TestTasteFallsBackToOverall(t *testing.T) {
	f := lisbon()
	f.TripTaste = nil
	f.OverallTaste = []Trait{
		{Key: "a", Label: "Bookshops", Score: 0.7, Confidence: 0.9},
		{Key: "b", Label: "Unsure thing", Score: 0.9, Confidence: 0.2},
	}
	rec, _, ok := NewBuilder().BuildTrip(f)
	require.True(t, ok)
	taste := card(rec, CardTaste).GetTaste()
	assert.False(t, taste.GetFromPeriod())
	require.Len(t, taste.GetTraits(), 1, "low-confidence traits stay out")
	assert.Zero(t, taste.GetTraits()[0].GetEvidenceCount())

	f.OverallTaste = nil
	rec, _, _ = NewBuilder().BuildTrip(f)
	assert.Nil(t, card(rec, CardTaste))
}

func TestMultiCityStampsAndRoute(t *testing.T) {
	f := lisbon()
	f.Cities = []City{
		{Name: "Lisbon", Lat: 38.72, Lon: -9.14, HasCoords: true},
		{Name: "Porto", Lat: 41.15, Lon: -8.61, HasCoords: true},
	}
	f.Places[3].CityName = "Porto"
	rec, _, ok := NewBuilder().BuildTrip(f)
	require.True(t, ok)

	groups := card(rec, CardPlaces).GetPlaces().GetGroups()
	assert.Equal(t, "Lisbon", groups[0].GetLabel(), "stamps are cities on a multi-city trip")
	assert.Contains(t, card(rec, CardPlaces).GetPlaces().GetHeadline(), "two cities")

	route := card(rec, CardRoute).GetRoute()
	assert.Equal(t, recapv1.RouteBasis_ROUTE_BASIS_CITIES, route.GetBasis())
	assert.InDelta(t, 274, route.GetDistanceKm(), 5)
	assert.Equal(t, "From Lisbon to Porto.", route.GetHeadline())
	assert.Equal(t, "Two cities, 5 days, one long story.", card(rec, CardIntro).GetIntro().GetLede())

	f.LegsKm = 313.4
	rec, _, _ = NewBuilder().BuildTrip(f)
	assert.Equal(t, recapv1.RouteBasis_ROUTE_BASIS_LEGS, card(rec, CardRoute).GetRoute().GetBasis())
	assert.InDelta(t, 313.4, card(rec, CardRoute).GetRoute().GetDistanceKm(), 0.01)
}

func TestShortCode(t *testing.T) {
	assert.Equal(t, "OPO", ShortCode("opo", "Porto"))
	assert.Equal(t, "MAL", ShortCode("", "Málaga"))
	assert.Equal(t, "SAO", ShortCode("", "São Paulo"))
	assert.Equal(t, "NEW", ShortCode("NY", "New York"), "two letters is not an airport")
	assert.Equal(t, "", ShortCode("", "東京"))
}

func TestPeriodLabel(t *testing.T) {
	assert.Equal(t, "Oct 2026", PeriodLabel(day("2026-10-01"), day("2026-10-09")))
	assert.Equal(t, "Sep–Oct 2026", PeriodLabel(day("2026-09-28"), day("2026-10-02")))
	assert.Equal(t, "Dec 2025 – Jan 2026", PeriodLabel(day("2025-12-30"), day("2026-01-02")))
}

func TestLongestRun(t *testing.T) {
	assert.Equal(t, 0, LongestRun(nil))
	assert.Equal(t, 1, LongestRun([]time.Time{day("2026-10-01")}))
	assert.Equal(t, 3, LongestRun([]time.Time{
		day("2026-10-05"), day("2026-10-01"), day("2026-10-02"), day("2026-10-03"), day("2026-10-03"),
	}))
}

func TestLowerFirstKeepsProperNouns(t *testing.T) {
	assert.Equal(t, "natural wine bars", lowerFirst("Natural wine bars"))
	assert.Equal(t, "LX Factory", lowerFirst("LX Factory"))
}
