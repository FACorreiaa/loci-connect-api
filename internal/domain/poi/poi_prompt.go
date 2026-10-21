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
	return fmt.Sprintf(`You are a travel assistant generating %s for a user at a specific location.

Requirements:
- User's location: latitude %.6f, longitude %.6f
- Search radius: %.1f kilometers. Include only places within it; these are shown as places near the user.
- Never return null. If nothing real is within the radius, return {"points_of_interest": []}; near a populated area there are usually several.

Generate up to 10 diverse %s.

Return as JSON:
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
		strictNote = "\n\nA previous attempt returned no results. Look harder: aim for at least 5 real, well-known places, and if the coordinates fall on a rural or sparsely populated spot, include notable places in the nearest town(s) inside the radius. Return an empty list only if no real place lies within the radius — never invent one to fill it."
	}

	return fmt.Sprintf(`You are a travel assistant generating points of interest for a user at a specific location.

Requirements:
- User's location: latitude %.6f, longitude %.6f
- Search radius: %.1f kilometers (%.0f meters). Include only places within it; these are shown as places near the user.
- Never return null. If nothing real is within the radius, return {"points_of_interest": []}; near a populated area there are usually several.

First, identify what town/city is at coordinates (%.4f, %.4f) and find POIs there and in immediate surroundings within %.1f km.

Generate up to 10 diverse points of interest including: restaurants, bars, hotels, museums, parks, historical sites, and activities.

Return as JSON:
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

Do not include popular places from other cities.%s`,
		lat, lon, radiusKm, distance,
		lat, lon, radiusKm,
		radiusKm, lat, radiusKm, lon,
		strictNote)
}
