package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

const (
	fixtureCity    = "Funchal"
	fixtureRequest = "3 days in winter with my parents"
	fixtureLat     = 32.65
	fixtureLon     = -16.91
)

// fixtureProfile is a fully populated profile with nothing random in it, so
// the rendered templates are the same on every run.
func fixtureProfile() *locitypes.UserPreferenceProfileResponse {
	lat, lon := 32.65, -16.91
	minStar, maxStar := 3.0, 5.0
	minNight, maxNight := 80.0, 200.0
	minMeal, maxMeal := 15.0, 60.0
	return &locitypes.UserPreferenceProfileResponse{
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
		Interests:            []*locitypes.Interest{{Name: "hiking"}, {Name: "museums"}},
		Tags:                 []*locitypes.Tags{{Name: "nightlife"}},
		UserLatitude:         &lat,
		UserLongitude:        &lon,
		AccommodationPreferences: &locitypes.AccommodationPreferences{
			AccommodationType:  []string{"guesthouse", "hotel"},
			StarRating:         &locitypes.RangeFilter{Min: &minStar, Max: &maxStar},
			PriceRangePerNight: &locitypes.RangeFilter{Min: &minNight, Max: &maxNight},
			Amenities:          []string{"breakfast", "wifi"},
			RoomType:           []string{"double"},
			ChainPreference:    "independent",
		},
		DiningPreferences: &locitypes.DiningPreferences{
			CuisineTypes:         []string{"local_specialty", "seafood"},
			MealTypes:            []string{"dinner", "lunch"},
			ServiceStyle:         []string{"casual"},
			PriceRangePerPerson:  &locitypes.RangeFilter{Min: &minMeal, Max: &maxMeal},
			AllergenFree:         []string{"nuts"},
			MichelinRated:        true,
			LocalRecommendations: true,
			ChainVsLocal:         "local_only",
			OrganicPreference:    true,
			OutdoorSeatingPref:   true,
		},
		ActivityPreferences: &locitypes.ActivityPreferences{
			ActivityCategories:     []string{"history", "nature"},
			PhysicalActivityLevel:  "moderate",
			IndoorOutdoorPref:      "mixed",
			CulturalImmersionLevel: "deep_local",
			MustSeeVsHiddenGems:    "mixed",
			EducationalPreference:  true,
			PhotoOpportunities:     true,
			SeasonSpecific:         []string{"winter_sports"},
			AvoidCrowds:            true,
			LocalEventsInterest:    []string{"festivals"},
		},
		ItineraryPreferences: &locitypes.ItineraryPreferences{
			PlanningStyle:         "flexible",
			TimeFlexibility:       "loose_schedule",
			MorningVsEvening:      "early_bird",
			WeekendVsWeekday:      "any",
			PreferredSeasons:      []string{"winter"},
			AvoidPeakSeason:       true,
			AdventureVsRelaxation: "balanced",
			SpontaneousVsPlanned:  "semi_planned",
		},
	}
}

// personalPrompts renders the four personal templates with the given request,
// each over the profile scoped to its own part — the way the plan pass will
// call them.
func personalPrompts(request string, profile *locitypes.UserPreferenceProfileResponse) map[generationPart]string {
	prefs := func(part generationPart) string {
		return getUserPreferencesPrompt(scopeProfileForPart(part, profile))
	}
	return map[generationPart]string{
		partItinerary:   getPersonalizedItineraryPrompt(fixtureCity, request, prefs(partItinerary)),
		partHotels:      getAccommodationPrompt(fixtureCity, fixtureLat, fixtureLon, request, prefs(partHotels)),
		partRestaurants: getDiningPrompt(fixtureCity, fixtureLat, fixtureLon, request, prefs(partRestaurants)),
		partActivities:  getActivitiesPrompt(fixtureCity, fixtureLat, fixtureLon, request, prefs(partActivities)),
	}
}

func TestTheRequestReachesEveryPersonalPrompt(t *testing.T) {
	for part, prompt := range personalPrompts(fixtureRequest, fixtureProfile()) {
		if !strings.Contains(prompt, "TRAVELLER'S REQUEST:\n"+fixtureRequest+"\n") {
			t.Errorf("%s: request text is not in the prompt", part)
		}
		if !strings.Contains(prompt, "honour any dates, season, occasion, duration, group or constraints") {
			t.Errorf("%s: planning instruction is missing", part)
		}
		// The block sits between the task line and the preferences, so the
		// model reads the request before the profile it must reconcile with.
		req, prefs := strings.Index(prompt, "TRAVELLER'S REQUEST:"), strings.Index(prompt, "USER PREFERENCES:")
		if req < 0 || prefs < 0 || req > prefs {
			t.Errorf("%s: request block is not before USER PREFERENCES", part)
		}
	}
}

func TestAnEmptyRequestRendersNoBlock(t *testing.T) {
	for _, empty := range []string{"", "   \n"} {
		for part, prompt := range personalPrompts(empty, fixtureProfile()) {
			if strings.Contains(prompt, "TRAVELLER'S REQUEST") {
				t.Errorf("%s: empty request %q still rendered a block", part, empty)
			}
			if !strings.Contains(prompt, "\nUSER PREFERENCES:\n") {
				t.Errorf("%s: preferences heading lost its own line", part)
			}
		}
	}
	if got := requestBlock("  "); got != "" {
		t.Errorf("requestBlock of whitespace = %q, want empty", got)
	}
}

// The city templates take no request and no profile; their text must not
// change because personal prompts learned to.
func TestCityPromptsAreUnchangedByTheRequest(t *testing.T) {
	for _, prompt := range []string{getCityDataPrompt(fixtureCity), getGeneralPOIPrompt(fixtureCity)} {
		if strings.Contains(prompt, "TRAVELLER'S REQUEST") || strings.Contains(prompt, "USER PREFERENCES") {
			t.Error("a city prompt carries personal sections")
		}
	}
}

func TestScopedProfileRendersWithoutNameOrStoredLocation(t *testing.T) {
	p := fixtureProfile()
	for part, prompt := range personalPrompts(fixtureRequest, p) {
		if strings.Contains(prompt, "Profile Name") || strings.Contains(prompt, p.ProfileName) {
			t.Errorf("%s: profile name reached the prompt", part)
		}
		if strings.Contains(prompt, "User Location") {
			t.Errorf("%s: stored location reached the prompt", part)
		}
	}

	// The unscoped renderer still shows both for callers that want them.
	full := getUserPreferencesPrompt(p)
	if !strings.Contains(full, "Profile Name: Winter break") || !strings.Contains(full, "User Location: 32.6500, -16.9100") {
		t.Error("unscoped profile lost its name or stored location")
	}
	if got := getUserPreferencesPrompt(&locitypes.UserPreferenceProfileResponse{}); strings.Contains(got, "Profile Name") {
		t.Error("an empty profile name still rendered a Profile Name line")
	}
}

func TestNilProfileRendersNothing(t *testing.T) {
	if got := getUserPreferencesPrompt(nil); got != "" {
		t.Errorf("nil profile rendered %q", got)
	}
}

func TestEachPartRendersOnlyItsOwnSection(t *testing.T) {
	prompts := personalPrompts(fixtureRequest, fixtureProfile())
	sections := map[generationPart][]string{
		partHotels:      {"ACCOMMODATION PREFERENCES"},
		partRestaurants: {"DINING PREFERENCES"},
		partActivities:  {"ACTIVITY PREFERENCES"},
		partItinerary:   {"ACCOMMODATION PREFERENCES", "DINING PREFERENCES", "ACTIVITY PREFERENCES", "ITINERARY PREFERENCES"},
	}
	all := []string{"ACCOMMODATION PREFERENCES", "DINING PREFERENCES", "ACTIVITY PREFERENCES", "ITINERARY PREFERENCES"}
	for part, want := range sections {
		prompt := prompts[part]
		wanted := map[string]bool{}
		for _, s := range want {
			wanted[s] = true
			if !strings.Contains(prompt, s) {
				t.Errorf("%s: missing %s", part, s)
			}
		}
		for _, s := range all {
			if !wanted[s] && strings.Contains(prompt, s) {
				t.Errorf("%s: renders %s, which its key does not cover", part, s)
			}
		}
	}
}

// pinnedTemplateVersion is the generationTemplateVersion these fingerprints
// were taken under. When a template changes, bump generationTemplateVersion
// in generation_key.go, then set this and the hashes below to the new values.
const pinnedTemplateVersion = "v2"

var pinnedTemplateFingerprints = map[generationPart]string{
	partCityData:    "8584eec11a770e0268d1d25fb68b0d1e91fcefd20fe39fe2cb1673d8a63268c8",
	partGeneralPOIs: "869a3a24de7e4ddebe6f6dfb3c3066f2004533d1eefa55e757e952455f5ea3a7",
	partItinerary:   "362e6e492a9c48bd52e61afb85992735c228669517b90ec239aae11121cd22f1",
	partHotels:      "d077111663a08cc09d67dab184957d328c9eee3d11a1cf07dbe3103b5a370e01",
	partRestaurants: "62b5abde8f95a29f14fa1366c6e5176d710e3846bb8cec1ae5117df5ae12ef12",
	partActivities:  "5ae4c11c99d428024e4b8892cc535839934e78b24636152eaea657be3c867d79",
}

func templateFingerprints() map[generationPart]string {
	out := personalPrompts(fixtureRequest, fixtureProfile())
	out[partCityData] = getCityDataPrompt(fixtureCity)
	out[partGeneralPOIs] = getGeneralPOIPrompt(fixtureCity)
	for part, prompt := range out {
		sum := sha256.Sum256([]byte(prompt))
		out[part] = hex.EncodeToString(sum[:])
	}
	return out
}

// A durable cache serves an answer produced by the template that existed when
// it was written. Changing a template without moving generationTemplateVersion
// would replay answers the new prompt would never have produced, so the
// rendered templates are pinned here and any drift must come with a bump.
func TestGenerationTemplateFingerprint(t *testing.T) {
	got := templateFingerprints()
	for part, want := range pinnedTemplateFingerprints {
		if got[part] == want {
			continue
		}
		if generationTemplateVersion == pinnedTemplateVersion {
			t.Errorf("the %s template changed (sha256 %s, pinned %s): bump generationTemplateVersion in generation_key.go, then update pinnedTemplateVersion and this fingerprint",
				part, got[part], want)
		} else {
			t.Errorf("the %s template changed and generationTemplateVersion is already %q: set pinnedTemplateVersion to it and pin sha256 %s",
				part, generationTemplateVersion, got[part])
		}
	}
	if generationTemplateVersion != pinnedTemplateVersion {
		t.Errorf("generationTemplateVersion is %q but the fingerprints were pinned under %q; update pinnedTemplateVersion",
			generationTemplateVersion, pinnedTemplateVersion)
	}
}
