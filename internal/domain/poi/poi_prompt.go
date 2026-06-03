package poi

import (
	"fmt"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func getRestaurantsNearbyPrompt(userLocation locitypes.UserLocation) string {
	if userLocation.SearchRadiusKm == 0 {
		userLocation.SearchRadiusKm = 5.0
	}
	return fmt.Sprintf(`
        Generate a list of up to 10 restaurants within %.2f km of coordinates %.2f, %.2f.
        Include a variety of restaurant categories to provide diverse options.
        The result must be in JSON format:
        {
            "restaurants": [
                {
                    "name": "Restaurant Name",
                    "latitude": <float>,
                    "longitude": <float>,
                    "category": "Restaurant|Bar|Cafe",
                    "description": "Brief description of the restaurant and its proximity to the user's location."
                }
            ]
        }
    `, userLocation.SearchRadiusKm, userLocation.UserLat, userLocation.UserLon)
}

func getHotelsNeabyPrompt(userLocation locitypes.UserLocation) string {
	return fmt.Sprintf(`
        Generate a list of maximum 10 hotels nearby the coordinates %0.2f , %0.2f.
        the hotels can be around %0.2f km radius from the user's location or if nothing provided, use the default radius of 5km.
        The hotels should be relevant to the user's interest.
        The result should be in the following JSON format:
        {
            "hotels": [
                {
                    "name": "Name of the Hotel",
                    "latitude": <float>,
                    "longitude": <float>,
                    "category": "Primary category (e.g., Hotel, Hostel, Guesthouse)",
                    "description": "A brief description of this hotel and why it's relevant to the user's interest."
                }
            ]
        }
    `, userLocation.UserLat, userLocation.UserLon, userLocation.SearchRadiusKm)
}

func getActivitiesNearbyPrompt(userLocation locitypes.UserLocation) string {
	if userLocation.SearchRadiusKm == 0 {
		userLocation.SearchRadiusKm = 5.0
	}
	return fmt.Sprintf(`
        Generate a list of up to 10 open air activities people can do within %.2f km of coordinates %.2f, %.2f.
        Include a variety of restaurant categories to provide diverse options.
        The result must be in JSON format:
        {
            "activities": [
                {
                    "name": "Activity Name",
                    "latitude": <float>,
                    "longitude": <float>,
                    "category": "category where it belong",
                    "description": "Brief description of the activity and its proximity to the user's location."
                }
            ]
        }
    `, userLocation.SearchRadiusKm, userLocation.UserLat, userLocation.UserLon)
}

func getAttractionsNeabyPrompt(userLocation locitypes.UserLocation) string {
	if userLocation.SearchRadiusKm == 0 {
		userLocation.SearchRadiusKm = 5.0
	}
	return fmt.Sprintf(`
        Generate a list of up to 10 attractions people can do within %.2f km of coordinates %.2f, %.2f.
        Include a variety of restaurant categories to provide diverse options.
        The result must be in JSON format:
        {
            "attractions": [
                {
                    "name": "Attractions Name",
                    "latitude": <float>,
                    "longitude": <float>,
                    "category": "category where it belong",
                    "description": "Brief description of the attractions and its proximity to the user's location."
                }
            ]
        }
    `, userLocation.SearchRadiusKm, userLocation.UserLat, userLocation.UserLon)
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
