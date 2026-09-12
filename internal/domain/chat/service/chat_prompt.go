package service

import (
	"fmt"
	"strings"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// getUserPreferencesPrompt renders a profile as the USER PREFERENCES block.
//
// It renders only what it is handed: a profile scoped by scopeProfileForPart
// has no name and no stored location, and neither line appears, which is what
// lets the profile snapshot hash stand for the rendered text. A nil profile
// renders as nothing at all.
func getUserPreferencesPrompt(searchProfile *locitypes.UserPreferenceProfileResponse) string {
	if searchProfile == nil {
		return ""
	}

	// Base preferences
	basePrefs := `
BASIC PREFERENCES:`
	if searchProfile.ProfileName != "" {
		basePrefs += fmt.Sprintf(`
    - Profile Name: %s`, searchProfile.ProfileName)
	}
	basePrefs += fmt.Sprintf(`
    - Search Radius: %.1f km
    - Preferred Time: %s
    - Budget Level: %d (0=any, 1=cheap, 4=expensive)
    - Prefers Outdoor Seating: %t
    - Prefers Dog Friendly: %t
    - Preferred Dietary Needs: [%s]
    - Preferred Pace: %s
    - Prefers Accessible POIs: %t
    - Preferred Vibes: [%s]
    - Preferred Transport: %s`,
		searchProfile.SearchRadiusKm, searchProfile.PreferredTime, searchProfile.BudgetLevel,
		searchProfile.PreferOutdoorSeating, searchProfile.PreferDogFriendly, strings.Join(searchProfile.DietaryNeeds, ", "),
		searchProfile.PreferredPace, searchProfile.PreferAccessiblePOIs, strings.Join(searchProfile.PreferredVibes, ", "),
		searchProfile.PreferredTransport)

	// User location if available
	if searchProfile.UserLatitude != nil && searchProfile.UserLongitude != nil {
		basePrefs += fmt.Sprintf(`
    - User Location: %.4f, %.4f`, *searchProfile.UserLatitude, *searchProfile.UserLongitude)
	}

	// Interests
	if len(searchProfile.Interests) > 0 {
		interests := make([]string, len(searchProfile.Interests))
		for i, interest := range searchProfile.Interests {
			interests[i] = interest.Name
		}
		basePrefs += fmt.Sprintf(`
    - Interests: [%s]`, strings.Join(interests, ", "))
	}

	// Tags to avoid
	if len(searchProfile.Tags) > 0 {
		tags := make([]string, len(searchProfile.Tags))
		for i, tag := range searchProfile.Tags {
			tags[i] = tag.Name
		}
		basePrefs += fmt.Sprintf(`
    - Tags to Avoid: [%s]`, strings.Join(tags, ", "))
	}

	// Accommodation preferences
	if searchProfile.AccommodationPreferences != nil {
		accom := searchProfile.AccommodationPreferences
		basePrefs += `

ACCOMMODATION PREFERENCES:`

		if len(accom.AccommodationType) > 0 {
			basePrefs += fmt.Sprintf(`
    - Accommodation Types: [%s]`, strings.Join(accom.AccommodationType, ", "))
		}

		if accom.StarRating != nil {
			minStar := "any"
			maxStar := "any"
			if accom.StarRating.Min != nil {
				minStar = fmt.Sprintf("%.0f", *accom.StarRating.Min)
			}
			if accom.StarRating.Max != nil {
				maxStar = fmt.Sprintf("%.0f", *accom.StarRating.Max)
			}
			basePrefs += fmt.Sprintf(`
    - Star Rating: %s - %s stars`, minStar, maxStar)
		}

		if accom.PriceRangePerNight != nil {
			minPrice := "any"
			maxPrice := "any"
			if accom.PriceRangePerNight.Min != nil {
				minPrice = fmt.Sprintf("%.0f", *accom.PriceRangePerNight.Min)
			}
			if accom.PriceRangePerNight.Max != nil {
				maxPrice = fmt.Sprintf("%.0f", *accom.PriceRangePerNight.Max)
			}
			basePrefs += fmt.Sprintf(`
    - Price Range Per Night: %s - %s`, minPrice, maxPrice)
		}

		if len(accom.Amenities) > 0 {
			basePrefs += fmt.Sprintf(`
    - Required Amenities: [%s]`, strings.Join(accom.Amenities, ", "))
		}

		if len(accom.RoomType) > 0 {
			basePrefs += fmt.Sprintf(`
    - Room Types: [%s]`, strings.Join(accom.RoomType, ", "))
		}

		if accom.ChainPreference != "" {
			basePrefs += fmt.Sprintf(`
    - Chain Preference: %s`, accom.ChainPreference)
		}
	}

	// Dining preferences
	if searchProfile.DiningPreferences != nil {
		dining := searchProfile.DiningPreferences
		basePrefs += `

DINING PREFERENCES:`

		if len(dining.CuisineTypes) > 0 {
			basePrefs += fmt.Sprintf(`
    - Cuisine Types: [%s]`, strings.Join(dining.CuisineTypes, ", "))
		}

		if len(dining.MealTypes) > 0 {
			basePrefs += fmt.Sprintf(`
    - Meal Types: [%s]`, strings.Join(dining.MealTypes, ", "))
		}

		if len(dining.ServiceStyle) > 0 {
			basePrefs += fmt.Sprintf(`
    - Service Style: [%s]`, strings.Join(dining.ServiceStyle, ", "))
		}

		if dining.PriceRangePerPerson != nil {
			minPrice := "any"
			maxPrice := "any"
			if dining.PriceRangePerPerson.Min != nil {
				minPrice = fmt.Sprintf("%.0f", *dining.PriceRangePerPerson.Min)
			}
			if dining.PriceRangePerPerson.Max != nil {
				maxPrice = fmt.Sprintf("%.0f", *dining.PriceRangePerPerson.Max)
			}
			basePrefs += fmt.Sprintf(`
    - Price Range Per Person: %s - %s`, minPrice, maxPrice)
		}

		if len(dining.AllergenFree) > 0 {
			basePrefs += fmt.Sprintf(`
    - Allergen Free: [%s]`, strings.Join(dining.AllergenFree, ", "))
		}

		if dining.MichelinRated {
			basePrefs += `
    - Michelin Rated: Preferred`
		}

		if dining.LocalRecommendations {
			basePrefs += `
    - Local Recommendations: Preferred`
		}

		if dining.ChainVsLocal != "" {
			basePrefs += fmt.Sprintf(`
    - Chain vs Local: %s`, dining.ChainVsLocal)
		}

		if dining.OrganicPreference {
			basePrefs += `
    - Organic Preference: Yes`
		}

		if dining.OutdoorSeatingPref {
			basePrefs += `
    - Outdoor Seating: Preferred`
		}
	}

	// Activity preferences
	if searchProfile.ActivityPreferences != nil {
		activity := searchProfile.ActivityPreferences
		basePrefs += `

ACTIVITY PREFERENCES:`

		if len(activity.ActivityCategories) > 0 {
			basePrefs += fmt.Sprintf(`
    - Activity Categories: [%s]`, strings.Join(activity.ActivityCategories, ", "))
		}

		if activity.PhysicalActivityLevel != "" {
			basePrefs += fmt.Sprintf(`
    - Physical Activity Level: %s`, activity.PhysicalActivityLevel)
		}

		if activity.IndoorOutdoorPref != "" {
			basePrefs += fmt.Sprintf(`
    - Indoor/Outdoor Preference: %s`, activity.IndoorOutdoorPref)
		}

		if activity.CulturalImmersionLevel != "" {
			basePrefs += fmt.Sprintf(`
    - Cultural Immersion Level: %s`, activity.CulturalImmersionLevel)
		}

		if activity.MustSeeVsHiddenGems != "" {
			basePrefs += fmt.Sprintf(`
    - Must-See vs Hidden Gems: %s`, activity.MustSeeVsHiddenGems)
		}

		if activity.EducationalPreference {
			basePrefs += `
    - Educational Preference: Yes`
		}

		if activity.PhotoOpportunities {
			basePrefs += `
    - Photography Opportunities: Important`
		}

		if len(activity.SeasonSpecific) > 0 {
			basePrefs += fmt.Sprintf(`
    - Season Specific: [%s]`, strings.Join(activity.SeasonSpecific, ", "))
		}

		if activity.AvoidCrowds {
			basePrefs += `
    - Avoid Crowds: Yes`
		}

		if len(activity.LocalEventsInterest) > 0 {
			basePrefs += fmt.Sprintf(`
    - Local Events Interest: [%s]`, strings.Join(activity.LocalEventsInterest, ", "))
		}
	}

	// Itinerary preferences
	if searchProfile.ItineraryPreferences != nil {
		itinerary := searchProfile.ItineraryPreferences
		basePrefs += `

ITINERARY PREFERENCES:`

		if itinerary.PlanningStyle != "" {
			basePrefs += fmt.Sprintf(`
    - Planning Style: %s`, itinerary.PlanningStyle)
		}

		if itinerary.TimeFlexibility != "" {
			basePrefs += fmt.Sprintf(`
    - Time Flexibility: %s`, itinerary.TimeFlexibility)
		}

		if itinerary.MorningVsEvening != "" {
			basePrefs += fmt.Sprintf(`
    - Morning vs Evening: %s`, itinerary.MorningVsEvening)
		}

		if itinerary.WeekendVsWeekday != "" {
			basePrefs += fmt.Sprintf(`
    - Weekend vs Weekday: %s`, itinerary.WeekendVsWeekday)
		}

		if len(itinerary.PreferredSeasons) > 0 {
			basePrefs += fmt.Sprintf(`
    - Preferred Seasons: [%s]`, strings.Join(itinerary.PreferredSeasons, ", "))
		}

		if itinerary.AvoidPeakSeason {
			basePrefs += `
    - Avoid Peak Season: Yes`
		}

		if itinerary.AdventureVsRelaxation != "" {
			basePrefs += fmt.Sprintf(`
    - Adventure vs Relaxation: %s`, itinerary.AdventureVsRelaxation)
		}

		if itinerary.SpontaneousVsPlanned != "" {
			basePrefs += fmt.Sprintf(`
    - Spontaneous vs Planned: %s`, itinerary.SpontaneousVsPlanned)
		}
	}

	return basePrefs
}

func getPOIDetailsPrompt(city string, lat, lon float64) string {
	return fmt.Sprintf(`
		Generate details for the following POI on the city of %s with the coordinates %0.2f , %0.2f.
		The result should be in the following JSON format:
		{
			"name": "Name of the Point of Interest",
			"description": "Detailed description of the POI and why it's relevant to the user's interest.",
    		"address": "address of the point of interest",
    		"website": "website of the POI if available",
    		"phone_number": "phone number of the POI if available",
    		"opening_hours": "Opening hours as string (e.g., 'Mon-Fri 9:00-17:00, Sat 10:00-15:00')"
    		"price_range": "price level if available",
            "category": "Primary category (e.g., Museum, Historical Site, Park, Restaurant, Bar)",
            "tags": ["tag1", "tag2", ...], -- Tags related to the POI
            "images": ["image_url_1", "image_url_2", ...], // images from wikipedia or pininterest
            "rating": <float> -- Average rating if available
            "stars": type of stars if available (e.g., "3 stars", "5 stars")

		}
	`, city, lat, lon)
}

func generatedContinuedConversationPrompt(poi, city string) string {
	return fmt.Sprintf(
		`Return ONLY a valid JSON object for "%s" in %s. Do not include any explanations, markdown formatting, or additional text.

Rules:
- If this is a Restaurant: include "cuisine_type" and omit "description_poi"
- If this is a Hotel: include "star_rating" and omit "description_poi"
- For other POIs: include "description_poi" (50-100 words)

Required JSON structure (return ONLY this, nothing else):
{
    "name": "string",
    "latitude": number,
    "longitude": number,
    "category": "string",
    "description_poi": "string"
}

If the POI is not found, return: {"name": "", "latitude": 0, "longitude": 0, "category": "", "description_poi": ""}`,
		poi, city,
	)
}

/*
  Testing Fan in Fan out prompt
*/

func getCityDataPrompt(cityName string) string {
	return fmt.Sprintf(`
You are a travel assistant. Provide general information about %s.
Respond ONLY WITH with JSON:
{
    "city": "%s",
    "country": "Country name",
    "state_province": "State/Province if applicable",
    "description": "Detailed city description (100-150 words)",
    "center_latitude": <float>,
    "center_longitude": <float>,
    "population": "",
    "area": "",
    "timezone": "",
    "language": "",
    "weather": "",
    "attractions": "",
    "history": ""
}`, cityName, cityName)
}

func getGeneralPOIPrompt(cityName, request string, target, days int, assumed bool) string {
	return fmt.Sprintf(`
List general points of interest in %s.
%s%s
Respond ONLY WITH with JSON:
{
    "points_of_interest": [
        {
            "name": "POI Name",
            "latitude": <float>,
            "longitude": <float>,
            "category": "Category (e.g., Museum, Historical Site)",
            "description_poi": "",
            "address": "",
            "website": "",
            "opening_hours": "e.g., 'Mon-Fri 9:00-17:00'"
        }
    ]
}`, cityName, requestBlock(request), countBlock(target, days, assumed, poiMix))
}

// requestBlock renders the traveller's own words into a personal prompt.
//
// Before this block existed the request text only steered domain detection
// and retrieval; "3 days in Funchal in winter" and "... in summer" reached
// the model as the same itinerary prompt and differed only by sampling. The
// block is what makes the request an input the cache key can honestly stand
// for. An empty request renders nothing.
func requestBlock(request string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		return ""
	}
	return fmt.Sprintf(`TRAVELLER'S REQUEST:
%s
Plan specifically around this request: honour any dates, season, occasion, duration, group or constraints it mentions, together with the preferences below.
`, request)
}

// poiMix and diningMix describe the shape a set of places has to have.
//
// The mix matters more than the number. A model handed only a count produces
// the same trip repeated — forty museums is not a longer stay, it is one
// afternoon copied out — so the proportions are stated, and a ceiling per
// category stops any one of them taking over.
const (
	poiMix = `MIX - these must cover a real trip, not one category:
- about 45%% anchors: the sights, viewpoints, museums and landmarks this place is known for
- about 30%% food and drink: a market, a cafe, a bakery, somewhere for dinner
- about 25%% neighbourhood time: a walkable street, a park, a beach, a lookout, a shop worth the detour
No more than three of any single category. Spread them across the area rather than stacking one district.`

	diningMix = `MIX - a week of eating, not one price bracket:
- mostly everyday places locals would actually use
- a few mid-range rooms worth booking
- at most one special-occasion restaurant
- include a cafe and a market or food hall`
)

// countBlock states how many places an answer must carry, over what horizon,
// and in what proportions.
//
// The honesty line at the end is load-bearing. Retrieval can only ground as
// many places as the corpus holds, so a thin city asked for forty will pad;
// telling the model that a short list is an acceptable answer is what makes the
// shortfall visible as a shortfall rather than as forty confident inventions.
func countBlock(target, days int, assumed bool, mix string) string {
	horizon := fmt.Sprintf("DURATION: %d days. Pace the list accordingly.", days)
	if assumed {
		horizon = "DURATION: none was given. Assume a 2-day sample: prefer iconic and high-fit places, not an exhaustive list."
	}

	return fmt.Sprintf(`HOW MANY:
Return %d places. Fewer is acceptable only if this city genuinely has no more worth the traveller's time - never pad with repeats or filler.

`+mix+`

%s
If you cannot find %d places of real quality, return fewer. A short honest list beats a long invented one.
`, target, horizon, target)
}

func getPersonalizedItineraryPrompt(cityName, request, basePreferences string, target, days int, assumed bool) string {
	return fmt.Sprintf(`
You are a travel planning assistant. Create a personalized itinerary for %s based on user preferences.
%s%sUSER PREFERENCES:
%s
Respond ONLY WITH with JSON:
{
    "itinerary_name": "Creative itinerary name",
    "overall_description": "Detailed description (100-150 words)",
    "points_of_interest": [
        {
            "name": "POI Name",
            "latitude": <float>,
            "longitude": <float>,
            "category": "",
            "description_poi": "",
            "address": "",
            "website": "",
                		"opening_hours": "Opening hours as string (e.g., 'Mon-Fri 9:00-17:00, Sat 10:00-15:00')"
,
            "distance": <float>,
            "day": <int, 1-based>
        }
    ]
}

Spread the places over the %d days in a sensible walking order: each day should
hold places near one another, and "day" must be between 1 and %d.`,
		cityName, requestBlock(request), countBlock(target, days, assumed, poiMix), basePreferences, days, days)
}

func getAccommodationPrompt(cityName string, lat, lon float64, request, basePreferences string) string {
	// When coordinates are (0,0), don't include them in the prompt as they confuse the LLM
	// (0,0) is in the Atlantic Ocean, not a valid city location
	var locationDesc string
	if lat != 0 || lon != 0 {
		locationDesc = fmt.Sprintf("in %s near coordinates %.4f, %.4f", cityName, lat, lon)
	} else {
		locationDesc = fmt.Sprintf("in %s", cityName)
	}

	return fmt.Sprintf(`
You are a hotel recommendation assistant. Find suitable accommodation %s.
Return 10 options: a stay is one hotel, so this does not grow with the trip.
%sUSER PREFERENCES:
%s
Respond ONLY WITH with JSON:
{
    "hotels": [
        {
            "city": "%s",
            "name": "Hotel Name",
            "latitude": <float>,
            "longitude": <float>,
            "category": "Hotel|Hostel|Guesthouse|Apartment",
            "description": "Description matching preferences",
            "address": "",
            "phone_number": nil,
            "website": nil,
            "opening_hours": "Opening hours as string (e.g., 'Mon-Fri 9:00-17:00, Sat 10:00-15:00')",
            "price_range": nil,
            "rating": 0,
            "tags": nil,
            "images": nil,
            "distance": <float>
        }
    ]
}`, locationDesc, requestBlock(request), basePreferences, cityName)
}

func getDiningPrompt(cityName string, lat, lon float64, request, basePreferences string, target, days int, assumed bool) string {
	// When coordinates are (0,0), don't include them in the prompt as they confuse the LLM
	var locationDesc string
	if lat != 0 || lon != 0 {
		locationDesc = fmt.Sprintf("in %s near coordinates %.4f, %.4f", cityName, lat, lon)
	} else {
		locationDesc = fmt.Sprintf("in %s", cityName)
	}

	return fmt.Sprintf(`
Find dining options %s.
%s%sUSER PREFERENCES:
%s
Respond with JSON:
{
    "restaurants": [
        {
            "city": "%s",
            "name": "Restaurant Name",
            "latitude": <float>,
            "longitude": <float>,
            "category": "Fine Dining|Casual Dining|Fast Food|Cafe|Bar",
            "description": "Description matching preferences",
            "address": "",
            "website": "",
            "phone_number": "",
            "opening_hours": "e.g., 'Mon-Fri 9:00-17:00'",
            "price_level": "$|$|$$|$$",
            "cuisine_type": "",
            "tags": [],
            "images": [],
            "rating": 0,
            "distance": <float>
        }
    ]
}`, locationDesc, requestBlock(request), countBlock(target, days, assumed, diningMix), basePreferences, cityName)
}

func getActivitiesPrompt(cityName string, lat, lon float64, request, basePreferences string, target, days int, assumed bool) string {
	// When coordinates are (0,0), don't include them in the prompt as they confuse the LLM
	var locationDesc string
	if lat != 0 || lon != 0 {
		locationDesc = fmt.Sprintf("in %s near coordinates %.4f, %.4f", cityName, lat, lon)
	} else {
		locationDesc = fmt.Sprintf("in %s", cityName)
	}

	return fmt.Sprintf(`
You are an activity recommendation assistant. Find activities %s.
%s%sUSER PREFERENCES:
%s
Respond ONLY WITH with JSON:
{
    "activities": [
        {
            "city": "%s",
            "name": "Activity Name",
            "latitude": <float>,
            "longitude": <float>,
            "category": "Museum|Outdoor Activity|Entertainment|Cultural|Sports",
            "description": "Description matching preferences",
            "address": "",
            "website": "",
            "opening_hours": "Opening hours as string (e.g., 'Mon-Fri 9:00-17:00, Sat 10:00-15:00')",
            "price_range": "Free|$|$$|$$$",
            "rating": 0,
            "tags": [],
            "images": [],
            "distance": <float>
        }
    ]
}`, locationDesc, requestBlock(request), countBlock(target, days, assumed, poiMix), basePreferences, cityName)
}
