//revive:disable-next-line:var-naming
package locitypes

type MainPageStatistics struct {
	TotalUsersCount       int64 `json:"total_users_count" db:"total_users_count"`
	TotalItinerariesSaved int64 `json:"total_itineraries_saved" db:"total_itineraries_saved"`
	TotalUniquePOIs       int64 `json:"total_unique_pois" db:"total_unique_pois"`
}

type DetailedPOIStatistics struct {
	GeneralPOIs   int64 `json:"general_pois" db:"general_pois"`
	SuggestedPOIs int64 `json:"suggested_pois" db:"suggested_pois"`
	Hotels        int64 `json:"hotels" db:"hotels"`
	Restaurants   int64 `json:"restaurants" db:"restaurants"`
	TotalPOIs     int64 `json:"total_pois" db:"total_pois"`
}

type LandingPageUserStats struct {
	SavedPlaces    int `json:"saved_places" db:"saved_places"`
	Itineraries    int `json:"itineraries" db:"itineraries"`
	CitiesExplored int `json:"cities_explored" db:"cities_explored"`
	Discoveries    int `json:"discoveries" db:"discoveries"`
}
