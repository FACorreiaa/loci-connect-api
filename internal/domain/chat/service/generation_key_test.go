package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func baseKeyInput() generationKeyInput {
	return generationKeyInput{
		Part:         partItinerary,
		Domain:       locitypes.DomainItinerary,
		CityID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		CityName:     "Funchal",
		ModelID:      "deepseek-v4-flash",
		Query:        "3 days in winter",
		SnapshotHash: "snap-a",
		UserID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Lat:          32.6501,
		Lon:          -16.9083,
		HasLocation:  true,
	}
}

func fullProfile() *locitypes.UserPreferenceProfileResponse {
	lat, lon := 32.65, -16.91
	minStar, maxStar := 3.0, 5.0
	return &locitypes.UserPreferenceProfileResponse{
		ID:                   uuid.New(),
		UserID:               uuid.New(),
		ProfileName:          "Winter break",
		IsDefault:            true,
		SearchRadiusKm:       5,
		PreferredTime:        locitypes.DayPreferenceDay,
		BudgetLevel:          2,
		PreferredPace:        locitypes.SearchPaceRelaxed,
		PreferAccessiblePOIs: true,
		PreferOutdoorSeating: true,
		PreferDogFriendly:    false,
		PreferredVibes:       []string{"quiet", "scenic"},
		PreferredTransport:   locitypes.TransportPreferenceWalk,
		DietaryNeeds:         []string{"vegetarian"},
		Interests:            []*locitypes.Interest{{ID: uuid.New(), Name: "hiking"}, {ID: uuid.New(), Name: "museums"}},
		Tags:                 []*locitypes.Tags{{ID: uuid.New(), Name: "nightlife"}},
		UserLatitude:         &lat,
		UserLongitude:        &lon,
		AccommodationPreferences: &locitypes.AccommodationPreferences{
			ID:                uuid.New(),
			AccommodationType: []string{"hotel", "guesthouse"},
			StarRating:        &locitypes.RangeFilter{Min: &minStar, Max: &maxStar},
			Amenities:         []string{"wifi", "breakfast"},
			ChainPreference:   "independent",
			CreatedAt:         time.Now(),
		},
		DiningPreferences: &locitypes.DiningPreferences{
			ID:                   uuid.New(),
			CuisineTypes:         []string{"local_specialty", "seafood"},
			MichelinRated:        false,
			LocalRecommendations: true,
			ChainVsLocal:         "local_only",
		},
		ActivityPreferences: &locitypes.ActivityPreferences{
			ID:                    uuid.New(),
			ActivityCategories:    []string{"nature", "history"},
			PhysicalActivityLevel: "moderate",
			SeasonSpecific:        []string{"winter_sports"},
			AvoidCrowds:           true,
		},
		ItineraryPreferences: &locitypes.ItineraryPreferences{
			ID:               uuid.New(),
			PlanningStyle:    "flexible",
			PreferredSeasons: []string{"winter"},
			AvoidPeakSeason:  true,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestBuildGenerationKeyIsDeterministic(t *testing.T) {
	a, b := buildGenerationKey(baseKeyInput()), buildGenerationKey(baseKeyInput())
	if a != b {
		t.Fatalf("same input, different keys: %s vs %s", a, b)
	}
	if !strings.HasPrefix(a, "gen:") || len(a) != len("gen:")+64 {
		t.Errorf("key has the wrong shape: %q", a)
	}
}

// Every input a personal part depends on must move the key; a cache that
// ignored any of them would cross-serve two different requests.
func TestEveryInputFlipsAPersonalKey(t *testing.T) {
	base := buildGenerationKey(baseKeyInput())
	variants := map[string]func(in *generationKeyInput){
		"part":     func(in *generationKeyInput) { in.Part = partHotels },
		"domain":   func(in *generationKeyInput) { in.Domain = locitypes.DomainGeneral },
		"city id":  func(in *generationKeyInput) { in.CityID = uuid.New() },
		"model":    func(in *generationKeyInput) { in.ModelID = "gemini-2.5-flash" },
		"query":    func(in *generationKeyInput) { in.Query = "3 days in summer" },
		"snapshot": func(in *generationKeyInput) { in.SnapshotHash = "snap-b" },
		"user":     func(in *generationKeyInput) { in.UserID = uuid.New() },
	}
	for name, mutate := range variants {
		in := baseKeyInput()
		mutate(&in)
		if got := buildGenerationKey(in); got == base {
			t.Errorf("changing %s did not change the key", name)
		}
	}
}

func TestCityNameStandsInOnlyWhenTheCityIDIsUnknown(t *testing.T) {
	in := baseKeyInput()
	in.CityID = uuid.Nil
	in.CityName = "Funchal"
	byName := buildGenerationKey(in)

	in.CityName = "  funchal "
	if got := buildGenerationKey(in); got != byName {
		t.Error("city name is not normalised when it stands in for the id")
	}
	in.CityName = "Lisbon"
	if got := buildGenerationKey(in); got == byName {
		t.Error("a different city name did not change the key")
	}

	// With an id known, the display name is irrelevant.
	in = baseKeyInput()
	withID := buildGenerationKey(in)
	in.CityName = "Funchal, Madeira"
	if got := buildGenerationKey(in); got != withID {
		t.Error("city name changed the key although the id was known")
	}
}

func TestQueryIsNormalisedInsideTheKey(t *testing.T) {
	in := baseKeyInput()
	in.Query = "3 days in winter"
	base := buildGenerationKey(in)
	in.Query = "  3 Days   in WINTER. "
	if got := buildGenerationKey(in); got != base {
		t.Error("case, spacing and trailing punctuation changed the key")
	}
}

// city_data and general_pois are shared: the prompt names only the city, so
// nothing about the traveller may reach the key.
func TestCityPartsIgnoreUserSnapshotAndLocation(t *testing.T) {
	for _, part := range []generationPart{partCityData, partGeneralPOIs} {
		in := baseKeyInput()
		in.Part = part
		base := buildGenerationKey(in)

		in.UserID = uuid.New()
		in.SnapshotHash = "someone else's profile"
		in.Lat, in.Lon = 0, 0
		in.HasLocation = false
		if got := buildGenerationKey(in); got != base {
			t.Errorf("%s: user, snapshot or location reached the key", part)
		}
	}
}

func TestOnlyLocationPartsCarryTheCoordinates(t *testing.T) {
	for _, part := range []generationPart{partHotels, partRestaurants, partActivities} {
		in := baseKeyInput()
		in.Part = part
		base := buildGenerationKey(in)

		in.Lat += 1
		if got := buildGenerationKey(in); got == base {
			t.Errorf("%s: moving a degree did not change the key", part)
		}
		in = baseKeyInput()
		in.Part = part
		in.HasLocation = false
		if got := buildGenerationKey(in); got == base {
			t.Errorf("%s: dropping the location did not change the key", part)
		}
	}

	in := baseKeyInput()
	in.Part = partItinerary
	base := buildGenerationKey(in)
	in.Lat += 1
	in.HasLocation = false
	if got := buildGenerationKey(in); got != base {
		t.Error("itinerary: the prompt never sees coordinates, yet the key did")
	}
}

// GPS jitter below ~110 m must not mint a new key; a real move must.
func TestCoordinatesRoundToThreeDecimals(t *testing.T) {
	in := baseKeyInput()
	in.Part = partRestaurants
	in.Lat, in.Lon = 32.6501, -16.9083
	base := buildGenerationKey(in)

	in.Lat, in.Lon = 32.65012, -16.90829
	if got := buildGenerationKey(in); got != base {
		t.Error("sub-3dp jitter changed the key")
	}
	in.Lat = 32.652
	if got := buildGenerationKey(in); got == base {
		t.Error("a 3dp move did not change the key")
	}

	if got := roundCoord(32.65049); got != 32.65 {
		t.Errorf("roundCoord(32.65049) = %v, want 32.65", got)
	}
	if got := roundCoord(-16.9085); got != -16.909 && got != -16.908 {
		t.Errorf("roundCoord(-16.9085) = %v", got)
	}
}

func TestHasLiveWords(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"what is open now in Funchal", true},
		{"things to do today", true},
		{"dinner tonight", true},
		{"what should I do right now", true},
		{"plans for this weekend", true},
		{"which museums are currently open", true},
		{"Open NOW near me", true},
		{"3 days in Funchal in winter", false},
		{"a snowy itinerary", false},   // "now" inside "snowy" is not a word
		{"the known landmarks", false}, // nor inside "known"
		{"", false},
	}
	for _, tc := range cases {
		if got := hasLiveWords(tc.text); got != tc.want {
			t.Errorf("hasLiveWords(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestNormalizeRequestText(t *testing.T) {
	cases := map[string]string{
		"  3 Days   in Funchal!! ": "3 days in funchal",
		"Winter trip.":             "winter trip",
		"":                         "",
		"...":                      "",
	}
	for in, want := range cases {
		if got := normalizeRequestText(in); got != want {
			t.Errorf("normalizeRequestText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScopeProfileIsNilForCityPartsAndNilInput(t *testing.T) {
	if scopeProfileForPart(partCityData, fullProfile()) != nil {
		t.Error("city_data scoped to a profile")
	}
	if scopeProfileForPart(partGeneralPOIs, fullProfile()) != nil {
		t.Error("general_pois scoped to a profile")
	}
	if scopeProfileForPart(partItinerary, nil) != nil {
		t.Error("nil profile scoped to a value")
	}
	if got := profileSnapshotHash(nil); got != "" {
		t.Errorf("snapshot hash of nil = %q, want empty", got)
	}
}

func TestScopeKeepsOnlyTheSectionThePartRenders(t *testing.T) {
	p := fullProfile()

	hotels := scopeProfileForPart(partHotels, p)
	if hotels.AccommodationPreferences == nil {
		t.Error("hotels lost accommodation preferences")
	}
	if hotels.DiningPreferences != nil || hotels.ActivityPreferences != nil || hotels.ItineraryPreferences != nil {
		t.Error("hotels kept a section its prompt does not render")
	}

	restaurants := scopeProfileForPart(partRestaurants, p)
	if restaurants.DiningPreferences == nil {
		t.Error("restaurants lost dining preferences")
	}
	if restaurants.AccommodationPreferences != nil || restaurants.ActivityPreferences != nil || restaurants.ItineraryPreferences != nil {
		t.Error("restaurants kept a section its prompt does not render")
	}

	activities := scopeProfileForPart(partActivities, p)
	if activities.ActivityPreferences == nil {
		t.Error("activities lost activity preferences")
	}
	if activities.AccommodationPreferences != nil || activities.DiningPreferences != nil || activities.ItineraryPreferences != nil {
		t.Error("activities kept a section its prompt does not render")
	}

	itinerary := scopeProfileForPart(partItinerary, p)
	if itinerary.AccommodationPreferences == nil || itinerary.DiningPreferences == nil ||
		itinerary.ActivityPreferences == nil || itinerary.ItineraryPreferences == nil {
		t.Error("itinerary dropped a section it renders")
	}

	// The base block is rendered by every personal part.
	for _, s := range []*locitypes.UserPreferenceProfileResponse{hotels, restaurants, activities, itinerary} {
		if s.BudgetLevel != p.BudgetLevel || s.SearchRadiusKm != p.SearchRadiusKm || len(s.Interests) != 2 || len(s.Tags) != 1 {
			t.Error("a base preference was lost in scoping")
		}
	}
}

// A hotels answer must not miss because the traveller edited their dining
// preferences: the hotels prompt never saw them.
func TestEditingAnUnrenderedSectionDoesNotMoveTheHash(t *testing.T) {
	p := fullProfile()
	before := profileSnapshotHash(scopeProfileForPart(partHotels, p))
	p.DiningPreferences.CuisineTypes = []string{"sushi"}
	p.ItineraryPreferences.PlanningStyle = "structured"
	if got := profileSnapshotHash(scopeProfileForPart(partHotels, p)); got != before {
		t.Error("dining/itinerary edits moved the hotels snapshot")
	}
	p.AccommodationPreferences.ChainPreference = "major_chains"
	if got := profileSnapshotHash(scopeProfileForPart(partHotels, p)); got == before {
		t.Error("an accommodation edit did not move the hotels snapshot")
	}
}

func TestIdentityNamingAndStoredLocationNeverReachTheHash(t *testing.T) {
	p := fullProfile()
	before := profileSnapshotHash(scopeProfileForPart(partItinerary, p))

	lat, lon := 38.72, -9.14
	p.ID = uuid.New()
	p.UserID = uuid.New()
	p.ProfileName = "Renamed"
	p.IsDefault = !p.IsDefault
	p.UserLatitude, p.UserLongitude = &lat, &lon
	p.CreatedAt = p.CreatedAt.Add(time.Hour)
	p.UpdatedAt = p.UpdatedAt.Add(time.Hour)
	p.Interests[0].ID = uuid.New()
	p.Tags[0].ID = uuid.New()
	p.AccommodationPreferences.ID = uuid.New()
	p.AccommodationPreferences.UpdatedAt = time.Now().Add(time.Hour)

	if got := profileSnapshotHash(scopeProfileForPart(partItinerary, p)); got != before {
		t.Error("identity, name, stored location or timestamps moved the snapshot hash")
	}

	scoped := scopeProfileForPart(partItinerary, p)
	if scoped.ProfileName != "" || scoped.UserLatitude != nil || scoped.UserLongitude != nil || scoped.ID != uuid.Nil {
		t.Error("scoped profile still carries identity or stored location")
	}
}

func TestSliceOrderAndDuplicatesDoNotMoveTheHash(t *testing.T) {
	p := fullProfile()
	before := profileSnapshotHash(scopeProfileForPart(partItinerary, p))

	p.PreferredVibes = []string{"scenic", "quiet", "quiet"}
	p.Interests = []*locitypes.Interest{{Name: "museums"}, {Name: "hiking"}, {Name: "hiking"}}
	p.AccommodationPreferences.AccommodationType = []string{"guesthouse", "hotel"}
	p.ActivityPreferences.ActivityCategories = []string{"history", "nature", "history"}
	if got := profileSnapshotHash(scopeProfileForPart(partItinerary, p)); got != before {
		t.Error("reordering or duplicating slice entries moved the snapshot hash")
	}

	p.PreferredVibes = []string{"loud"}
	if got := profileSnapshotHash(scopeProfileForPart(partItinerary, p)); got == before {
		t.Error("a changed slice entry did not move the snapshot hash")
	}
}

func TestEmptyAndNilSlicesHashAlike(t *testing.T) {
	p := fullProfile()
	p.PreferredVibes = nil
	p.DietaryNeeds = nil
	a := profileSnapshotHash(scopeProfileForPart(partItinerary, p))
	p.PreferredVibes = []string{}
	p.DietaryNeeds = []string{}
	if b := profileSnapshotHash(scopeProfileForPart(partItinerary, p)); a != b {
		t.Error("nil and empty slices produced different snapshots")
	}
}

func TestPartHelpers(t *testing.T) {
	for _, p := range []generationPart{partCityData, partGeneralPOIs} {
		if isPersonalPart(p) || partUsesLocation(p) {
			t.Errorf("%s is neither personal nor located", p)
		}
	}
	if !isPersonalPart(partItinerary) || partUsesLocation(partItinerary) {
		t.Error("itinerary is personal and not located")
	}
	for _, p := range []generationPart{partHotels, partRestaurants, partActivities} {
		if !isPersonalPart(p) || !partUsesLocation(p) {
			t.Errorf("%s is personal and located", p)
		}
	}
	if partTTL(partCityData) != 30*24*time.Hour || partTTL(partGeneralPOIs) != 7*24*time.Hour ||
		partTTL(partItinerary) != 14*24*time.Hour || partTTL(partHotels) != 7*24*time.Hour {
		t.Error("part TTLs drifted from the plan")
	}
}

func TestExtractionCacheKeyIsGlobalAndTrimmed(t *testing.T) {
	a := extractionCacheKey("3 days in Funchal")
	if a != extractionCacheKey("  3 days in Funchal\n") {
		t.Error("surrounding whitespace changed the extraction key")
	}
	if a == extractionCacheKey("3 days in funchal") {
		t.Error("extraction key folded case; the extractor sees the raw text")
	}
	if !strings.HasPrefix(a, "cityx:") {
		t.Errorf("unexpected prefix: %q", a)
	}
}

// The resolved count is in the key because it depends on the caller's plan: a
// free caller asks for forty places and a Pro caller for fifty, from the same
// words. Without this component the first answer to arrive would be replayed to
// both.
func TestPOITargetSeparatesKeysForThePartsThatRenderIt(t *testing.T) {
	base := generationKeyInput{
		Domain:    locitypes.DomainItinerary,
		CityID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ModelID:   "gemini-test",
		Query:     "a month in madeira",
		UserID:    uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		POITarget: 40,
	}

	for _, part := range []generationPart{partGeneralPOIs, partItinerary, partRestaurants, partActivities} {
		free, pro := base, base
		free.Part, pro.Part = part, part
		pro.POITarget = 50
		if buildGenerationKey(free) == buildGenerationKey(pro) {
			t.Errorf("%s: a 40-place and a 50-place answer share a key", part)
		}
	}

	// City data is one description of a city however long the stay, and a
	// month-long stay is still one hotel. Putting the count in either key would
	// fragment their entries for no gain.
	for _, part := range []generationPart{partCityData, partHotels} {
		free, pro := base, base
		free.Part, pro.Part = part, part
		pro.POITarget = 50
		if buildGenerationKey(free) != buildGenerationKey(pro) {
			t.Errorf("%s: the count fragments a key whose prompt does not render it", part)
		}
	}
}

// Every part that carries the count in its key must render it in its prompt,
// and vice versa. The two lists drifting apart is what made general_pois store
// duplicate lists for months: its key varied with the request text that its
// prompt ignored.
func TestPartUsesPOITargetMatchesThePrompts(t *testing.T) {
	rendersCount := map[generationPart]bool{
		partCityData:    false,
		partGeneralPOIs: true,
		partItinerary:   true,
		partHotels:      false,
		partRestaurants: true,
		partActivities:  true,
	}
	for part, want := range rendersCount {
		if got := partUsesPOITarget(part); got != want {
			t.Errorf("partUsesPOITarget(%s) = %v, want %v", part, got, want)
		}
	}
}

// A golden key pins where the count sits in the hash. Appending a component in
// the wrong place silently reshuffles every key, which is a cache miss for
// everybody rather than a test failure.
func TestGenerationKeyIsStable(t *testing.T) {
	in := generationKeyInput{
		Part:         partItinerary,
		Domain:       locitypes.DomainItinerary,
		CityID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ModelID:      "gemini-test",
		Query:        "4 days in madeira",
		SnapshotHash: "snapshot",
		UserID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		POITarget:    24,
	}
	const want = "gen:51867f217e7ee71437afc20dc2d3507132e35f78f9cbaa6224e480524bd15049"
	if got := buildGenerationKey(in); got != want {
		t.Errorf("generation key = %q, want %q", got, want)
	}
}
