package poi

import (
	"fmt"
)

// domainNearbyPrompt builds a location prompt for a specific POI domain. It
// uses the same JSON contract the workers parse ("points_of_interest" with
// description_poi), converts the distance (meters) to km, and forbids null —
// the previous per-domain prompts emitted a mismatched key and treated the
// meter distance as km, so the LLM fallback always returned nothing.
func domainNearbyPrompt(domainNoun, categoryHint string, lat, lon, distance float64) string {
	radiusKm := distance / 1000
	if radiusKm <= 0 {
		radiusKm = 5.0
	}
	return fmt.Sprintf(`You are a travel assistant generating %s for a user at a SPECIFIC location.

CRITICAL REQUIREMENTS:
- User's EXACT location: latitude %.6f, longitude %.6f
- Search radius: %.1f kilometers
- ONLY include places WITHIN this radius from the user's coordinates
- NEVER return null. If you truly cannot find anything, return {"points_of_interest": []} — but you should almost always find several near a populated area.

Generate up to 10 diverse %s.

Return STRICTLY as JSON:
{
  "points_of_interest": [
    {
      "name": "Place Name",
      "latitude": <float within %.1f km of %.6f>,
      "longitude": <float within %.1f km of %.6f>,
      "category": "%s",
      "description_poi": "2-3 sentence description of this specific place."
    }
  ]
}`,
		domainNoun, lat, lon, radiusKm, domainNoun,
		radiusKm, lat, radiusKm, lon, categoryHint)
}

func getRestaurantsNearbyPrompt(lat, lon, distance float64) string {
	return domainNearbyPrompt("restaurants, bars, and cafes", "Restaurant, Bar, or Cafe", lat, lon, distance)
}

func getHotelsNeabyPrompt(lat, lon, distance float64) string {
	return domainNearbyPrompt("hotels and places to stay", "Hotel, Hostel, or Guesthouse", lat, lon, distance)
}

func getActivitiesNearbyPrompt(lat, lon, distance float64) string {
	return domainNearbyPrompt("activities and things to do", "Activity category (e.g. Outdoor, Sport, Tour)", lat, lon, distance)
}

func getAttractionsNeabyPrompt(lat, lon, distance float64) string {
	return domainNearbyPrompt("attractions and sights", "Attraction category (e.g. Museum, Landmark, Park)", lat, lon, distance)
}

func getGeneralPOIByDistancePrompt(lat, lon, distance float64, strict bool) string {
	radiusKm := distance / 1000

	strictNote := ""
	if strict {
		strictNote = "\n\nRETRY INSTRUCTION: A previous attempt returned no results. You MUST return at least 5 real, well-known points of interest. Never return null or an empty array. If the exact coordinates fall on a rural or sparsely populated spot, include notable POIs from the nearest town(s) that still fall within the radius."
	}

	return fmt.Sprintf(`You are a travel assistant generating points of interest for a user at a SPECIFIC location.

CRITICAL REQUIREMENTS:
- User's EXACT location: latitude %.6f, longitude %.6f
- Search radius: %.1f kilometers (%.0f meters)
- ONLY include POIs that are WITHIN this radius from the user's coordinates
- Calculate the approximate distance from the user's coordinates before including each POI
- NEVER return null. If you truly cannot find anything, return {"points_of_interest": []} — but you should almost always be able to find several real places near a populated area.

First, identify what town/city is at coordinates (%.4f, %.4f) and find POIs there and in immediate surroundings within %.1f km.

Generate up to 10 diverse points of interest including: restaurants, bars, hotels, museums, parks, historical sites, and activities.

Return STRICTLY as JSON:
{
  "points_of_interest": [
    {
      "name": "POI Name",
      "latitude": <float within %.1f km of %.6f>,
      "longitude": <float within %.1f km of %.6f>,
      "category": "Category (Museum, Restaurant, Park, etc.)",
      "description_poi": "2-3 sentence description of this specific POI."
    }
  ]
}

IMPORTANT: Every POI latitude/longitude MUST be within %.1f km of (%.6f, %.6f). Do not include popular POIs from other cities.%s`,
		lat, lon, radiusKm, distance,
		lat, lon, radiusKm,
		radiusKm, lat, radiusKm, lon,
		radiusKm, lat, lon, strictNote)
}
