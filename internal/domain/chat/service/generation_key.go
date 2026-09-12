package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// generationTemplateVersion is the first component of every generation cache
// key.
//
// BUMP ME whenever any prompt template in chat_prompt.go changes — the six
// part templates or getUserPreferencesPrompt/requestBlock that feed them. A
// cached answer was produced by the template that existed when it was
// written; serving it under a new template would replay text the new prompt
// would never have produced. TestGenerationTemplateFingerprint pins the
// rendered templates and fails until this constant moves.
const generationTemplateVersion = "v3"

// generationLanguage is the language every template is written in today. It
// sits in the key so that localised templates, when they exist, cannot serve
// each other's answers.
const generationLanguage = "en"

// generationPart names one streamed part of a chat answer. Each part has its
// own prompt, its own cache key and its own lifetime.
type generationPart string

const (
	partCityData    generationPart = "city_data"
	partGeneralPOIs generationPart = "general_pois"
	partItinerary   generationPart = "itinerary"
	partHotels      generationPart = "hotels"
	partRestaurants generationPart = "restaurants"
	partActivities  generationPart = "activities"
)

// isPersonalPart reports whether the part's prompt carries anything about the
// traveller: the preference profile and, through the evidence packet, the
// visited flags. Personal parts are keyed per user and per profile snapshot;
// the city parts are shared by everyone because their prompts only name the
// city.
func isPersonalPart(p generationPart) bool {
	switch p {
	case partItinerary, partHotels, partRestaurants, partActivities:
		return true
	}
	return false
}

// partUsesLocation reports whether the part's prompt interpolates the request
// coordinates ("near coordinates 32.6500, -16.9100"). Those parts carry the
// rounded location in their key; the others must not, or a location change
// would miss on an answer that did not depend on it.
func partUsesLocation(p generationPart) bool {
	switch p {
	case partHotels, partRestaurants, partActivities:
		return true
	}
	return false
}

// partUsesPOITarget reports whether the part's prompt renders the resolved
// count, and so whether its key has to carry it.
//
// City data does not: one description of a city is one description whatever
// the trip length, and folding the count in would fragment a 30-day entry into
// one row per target for nothing. Accommodation does not either — a month-long
// stay is still one hotel, so that prompt asks for a fixed ten.
//
// The count belongs in the key at all because it depends on the caller's plan.
// Without it, a free caller's forty places would be replayed to somebody paying
// for fifty.
func partUsesPOITarget(p generationPart) bool {
	switch p {
	case partGeneralPOIs, partItinerary, partRestaurants, partActivities:
		return true
	}
	return false
}

// partTTL is how long a durable generation stays servable. City facts barely
// move; place lists drift with ingest; a personal itinerary is tied to a
// profile snapshot that self-invalidates on edit, so it can live longer than
// the place lists it was built from.
func partTTL(p generationPart) time.Duration {
	switch p {
	case partCityData:
		return 30 * 24 * time.Hour
	case partItinerary:
		return 14 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

// scopeProfileForPart reduces a profile to exactly what the part's prompt
// renders, so that the same value can feed both the prompt and the snapshot
// hash. The key then depends on the prompt's inputs by construction rather
// than by a second allowlist that could drift.
//
// City parts and a nil profile scope to nil. Otherwise the copy has every
// identity, naming, timestamp and stored-location field zeroed (none of them
// is rendered once the prompt guards them), every string slice sorted and
// de-duplicated, interests and tags reduced to their names, and only the
// sub-preference section that part renders kept: hotels keep accommodation,
// restaurants keep dining, activities keep activity, and the itinerary keeps
// all four.
func scopeProfileForPart(part generationPart, profile *locitypes.UserPreferenceProfileResponse) *locitypes.UserPreferenceProfileResponse {
	if profile == nil || !isPersonalPart(part) {
		return nil
	}

	scoped := &locitypes.UserPreferenceProfileResponse{
		SearchRadiusKm:       profile.SearchRadiusKm,
		PreferredTime:        profile.PreferredTime,
		BudgetLevel:          profile.BudgetLevel,
		PreferredPace:        profile.PreferredPace,
		PreferAccessiblePOIs: profile.PreferAccessiblePOIs,
		PreferOutdoorSeating: profile.PreferOutdoorSeating,
		PreferDogFriendly:    profile.PreferDogFriendly,
		PreferredVibes:       canonicalStrings(profile.PreferredVibes),
		PreferredTransport:   profile.PreferredTransport,
		DietaryNeeds:         canonicalStrings(profile.DietaryNeeds),
		Interests:            canonicalInterests(profile.Interests),
		Tags:                 canonicalTags(profile.Tags),
	}

	keepAll := part == partItinerary
	if keepAll || part == partHotels {
		scoped.AccommodationPreferences = scopeAccommodation(profile.AccommodationPreferences)
	}
	if keepAll || part == partRestaurants {
		scoped.DiningPreferences = scopeDining(profile.DiningPreferences)
	}
	if keepAll || part == partActivities {
		scoped.ActivityPreferences = scopeActivity(profile.ActivityPreferences)
	}
	if keepAll {
		scoped.ItineraryPreferences = scopeItinerary(profile.ItineraryPreferences)
	}
	return scoped
}

func scopeAccommodation(in *locitypes.AccommodationPreferences) *locitypes.AccommodationPreferences {
	if in == nil {
		return nil
	}
	return &locitypes.AccommodationPreferences{
		AccommodationType:  canonicalStrings(in.AccommodationType),
		StarRating:         copyRange(in.StarRating),
		PriceRangePerNight: copyRange(in.PriceRangePerNight),
		Amenities:          canonicalStrings(in.Amenities),
		RoomType:           canonicalStrings(in.RoomType),
		ChainPreference:    in.ChainPreference,
		CancellationPolicy: canonicalStrings(in.CancellationPolicy),
		BookingFlexibility: in.BookingFlexibility,
	}
}

func scopeDining(in *locitypes.DiningPreferences) *locitypes.DiningPreferences {
	if in == nil {
		return nil
	}
	return &locitypes.DiningPreferences{
		CuisineTypes:         canonicalStrings(in.CuisineTypes),
		MealTypes:            canonicalStrings(in.MealTypes),
		ServiceStyle:         canonicalStrings(in.ServiceStyle),
		PriceRangePerPerson:  copyRange(in.PriceRangePerPerson),
		DietaryNeeds:         canonicalStrings(in.DietaryNeeds),
		AllergenFree:         canonicalStrings(in.AllergenFree),
		MichelinRated:        in.MichelinRated,
		LocalRecommendations: in.LocalRecommendations,
		ChainVsLocal:         in.ChainVsLocal,
		OrganicPreference:    in.OrganicPreference,
		OutdoorSeatingPref:   in.OutdoorSeatingPref,
	}
}

func scopeActivity(in *locitypes.ActivityPreferences) *locitypes.ActivityPreferences {
	if in == nil {
		return nil
	}
	return &locitypes.ActivityPreferences{
		ActivityCategories:     canonicalStrings(in.ActivityCategories),
		PhysicalActivityLevel:  in.PhysicalActivityLevel,
		IndoorOutdoorPref:      in.IndoorOutdoorPref,
		CulturalImmersionLevel: in.CulturalImmersionLevel,
		MustSeeVsHiddenGems:    in.MustSeeVsHiddenGems,
		EducationalPreference:  in.EducationalPreference,
		PhotoOpportunities:     in.PhotoOpportunities,
		SeasonSpecific:         canonicalStrings(in.SeasonSpecific),
		AvoidCrowds:            in.AvoidCrowds,
		LocalEventsInterest:    canonicalStrings(in.LocalEventsInterest),
	}
}

func scopeItinerary(in *locitypes.ItineraryPreferences) *locitypes.ItineraryPreferences {
	if in == nil {
		return nil
	}
	return &locitypes.ItineraryPreferences{
		PlanningStyle:         in.PlanningStyle,
		PreferredPace:         in.PreferredPace,
		TimeFlexibility:       in.TimeFlexibility,
		MorningVsEvening:      in.MorningVsEvening,
		WeekendVsWeekday:      in.WeekendVsWeekday,
		PreferredSeasons:      canonicalStrings(in.PreferredSeasons),
		AvoidPeakSeason:       in.AvoidPeakSeason,
		AdventureVsRelaxation: in.AdventureVsRelaxation,
		SpontaneousVsPlanned:  in.SpontaneousVsPlanned,
	}
}

func copyRange(in *locitypes.RangeFilter) *locitypes.RangeFilter {
	if in == nil {
		return nil
	}
	out := &locitypes.RangeFilter{}
	if in.Min != nil {
		v := *in.Min
		out.Min = &v
	}
	if in.Max != nil {
		v := *in.Max
		out.Max = &v
	}
	return out
}

// canonicalStrings sorts and de-duplicates a slice, and folds an empty one to
// nil so that "no values" has one JSON spelling whichever way it was loaded.
func canonicalStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// canonicalInterests keeps only the names — the one field the prompt renders —
// as fresh values, so identifiers and timestamps never reach the hash.
func canonicalInterests(in []*locitypes.Interest) []*locitypes.Interest {
	names := make([]string, 0, len(in))
	for _, i := range in {
		if i != nil {
			names = append(names, i.Name)
		}
	}
	names = canonicalStrings(names)
	if names == nil {
		return nil
	}
	out := make([]*locitypes.Interest, len(names))
	for i, n := range names {
		out[i] = &locitypes.Interest{Name: n}
	}
	return out
}

func canonicalTags(in []*locitypes.Tags) []*locitypes.Tags {
	names := make([]string, 0, len(in))
	for _, t := range in {
		if t != nil {
			names = append(names, t.Name)
		}
	}
	names = canonicalStrings(names)
	if names == nil {
		return nil
	}
	out := make([]*locitypes.Tags, len(names))
	for i, n := range names {
		out[i] = &locitypes.Tags{Name: n}
	}
	return out
}

// profileSnapshotHash fingerprints a scoped profile. It is "" for nil — the
// city parts, and a traveller with personalisation off — so those keys carry
// an empty snapshot rather than a hash of nothing.
func profileSnapshotHash(scoped *locitypes.UserPreferenceProfileResponse) string {
	if scoped == nil {
		return ""
	}
	raw, err := json.Marshal(scoped)
	if err != nil {
		// The struct is plain data; Marshal cannot fail on it. Should it ever,
		// a stable non-empty marker is safer than a panic on the chat path.
		return "unhashable"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// normalizeRequestText folds the traveller's request into the form the key
// hashes: lower-cased, whitespace collapsed, trailing punctuation dropped, so
// "3 days in Funchal." and "3 days in funchal" are the same request.
func normalizeRequestText(s string) string {
	s = normalizeCacheComponent(s)
	s = strings.TrimRight(s, ".,;:!?")
	return strings.TrimSpace(s)
}

// liveWordsRE matches phrasing that pins a request to the present moment. An
// answer to "what is open right now" is wrong an hour later, so such requests
// are never served from a durable cache.
var liveWordsRE = regexp.MustCompile(`(?i)\b(today|tonight|right now|now|this weekend|open now|currently)\b`)

// hasLiveWords reports whether the request text refers to the present moment.
func hasLiveWords(s string) bool {
	return liveWordsRE.MatchString(s)
}

// roundCoord rounds a coordinate to three decimals (about 110 m), which is
// finer than the prompt's own four-decimal rendering needs to be distinct and
// coarse enough that GPS jitter does not mint a new key per request.
func roundCoord(v float64) float64 {
	return math.Round(v*1000) / 1000
}

// generationKeyInput is everything a generation key may depend on. Which
// fields actually reach the hash depends on the part: see buildGenerationKey.
type generationKeyInput struct {
	Part   generationPart
	Domain locitypes.DomainType
	// CityID identifies the city when it resolved in the database; otherwise
	// the normalised CityName stands in for it.
	CityID   uuid.UUID
	CityName string
	// ModelID is the model the router plans to answer with for this request.
	ModelID string
	// Query is the traveller's request text (normalised here).
	Query string
	// SnapshotHash is profileSnapshotHash of the part's scoped profile. It is
	// ignored for city parts, whose prompts hold no profile.
	SnapshotHash string
	// UserID separates personal parts: the evidence packet renders this
	// user's visited flags into them.
	UserID uuid.UUID
	// Lat/Lon are the request coordinates; HasLocation says whether any were
	// supplied. Only parts whose prompt interpolates them include them.
	Lat, Lon    float64
	HasLocation bool
	// POITarget is how many places the answer was asked for. Only parts whose
	// prompt renders it include it; see partUsesPOITarget.
	POITarget int
}

// buildGenerationKey derives the cache key for one part of one answer:
//
//	"gen:" + sha256(version \0 part \0 domain \0 city \0 model \0 language
//	                \0 query \0 snapshot [\0 user] [\0 lat \0 lon])
//
// The user id is appended for personal parts only and the rounded
// coordinates for location parts only, so the two shared city parts hash to
// the same key for every traveller. The evidence packet is deliberately
// absent: it is recorded next to the answer for audit, not folded into its
// identity, otherwise every POI ingest would miss on every cached answer.
func buildGenerationKey(in generationKeyInput) string {
	city := normalizeCacheComponent(in.CityName)
	if in.CityID != uuid.Nil {
		city = in.CityID.String()
	}

	snapshot := in.SnapshotHash
	if !isPersonalPart(in.Part) {
		snapshot = ""
	}

	components := []string{
		generationTemplateVersion,
		string(in.Part),
		string(in.Domain),
		city,
		in.ModelID,
		generationLanguage,
		normalizeRequestText(in.Query),
		snapshot,
	}
	if isPersonalPart(in.Part) {
		components = append(components, in.UserID.String())
	}
	if partUsesPOITarget(in.Part) {
		components = append(components, strconv.Itoa(in.POITarget))
	}
	if partUsesLocation(in.Part) && in.HasLocation {
		components = append(components,
			fmt.Sprintf("%.3f", roundCoord(in.Lat)),
			fmt.Sprintf("%.3f", roundCoord(in.Lon)))
	}

	sum := sha256.Sum256([]byte(strings.Join(components, "\x00")))
	return "gen:" + hex.EncodeToString(sum[:])
}

// extractionCacheKey keys the city-extraction step by the raw message alone.
// Extraction reads nothing but the text, so the entry is global and carries
// no user data.
func extractionCacheKey(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return "cityx:" + hex.EncodeToString(sum[:])
}
