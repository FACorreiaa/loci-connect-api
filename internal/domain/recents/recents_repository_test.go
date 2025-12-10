package recents

import (
	"context"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Helper function to create a string pointer
func strPtr(s string) *string {
	return &s
}

// Helper function to create an int32 pointer
func int32Ptr(i int32) *int32 {
	return &i
}

func TestGetCityPOIsByInteraction(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	cityName := "Paris"
	now := time.Now()
	poiID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT
			pd.id,
			pd.name,
			pd.latitude,
			pd.longitude,
			pd.description,
			pd.address,
			pd.website,
			pd.phone_number,
			pd.opening_hours,
			pd.price_range,
			pd.category,
			pd.tags,
			pd.images,
			pd.rating,
			pd.created_at
		FROM poi_details pd
		JOIN llm_interactions li ON pd.llm_interaction_id = li.id
		WHERE li.user_id = $1 AND li.city_name = $2
		ORDER BY pd.created_at DESC
	`)).
		WithArgs(userID, cityName).
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "name", "latitude", "longitude", "description", "address", "website", "phone_number", "opening_hours", "price_range", "category", "tags", "images", "rating", "created_at"}).
				AddRow(poiID, "Eiffel Tower", 48.8584, 2.2945, strPtr("Famous landmark"), strPtr("Champ de Mars"), strPtr("https://www.toureiffel.paris"), strPtr("+33123456789"), strPtr("9:00-23:00"), strPtr("$$"), strPtr("landmark"), []string{"tourist", "landmark"}, []string{"image1.jpg"}, 4.5, now),
		)

	repo := NewRepository(mock, slog.Default())
	pois, err := repo.GetCityPOIsByInteraction(context.Background(), userID, cityName)
	if err != nil {
		t.Fatalf("GetCityPOIsByInteraction: %v", err)
	}

	if len(pois) != 1 {
		t.Fatalf("expected 1 POI, got %d", len(pois))
	}
	if pois[0].ID != poiID {
		t.Errorf("expected POI ID %s, got %s", poiID, pois[0].ID)
	}
	if pois[0].Name != "Eiffel Tower" {
		t.Errorf("expected POI name 'Eiffel Tower', got %s", pois[0].Name)
	}
	if pois[0].OpeningHours["general"] != "9:00-23:00" {
		t.Errorf("expected opening hours '9:00-23:00', got %v", pois[0].OpeningHours)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetCityHotelsByInteraction(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	cityName := "Paris"
	hotelID := uuid.New()
	llmInteractionID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT
			hd.id,
			hd.name,
			hd.latitude,
			hd.longitude,
			hd.category,
			hd.description,
			hd.address,
			hd.website,
			hd.phone_number,
			hd.price_range,
			hd.tags,
			hd.images,
			hd.rating,
			hd.llm_interaction_id
		FROM hotel_details hd
		JOIN llm_interactions li ON hd.llm_interaction_id = li.id
		WHERE li.user_id = $1 AND li.city_name = $2
		ORDER BY hd.created_at DESC
	`)).
		WithArgs(userID, cityName).
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "name", "latitude", "longitude", "category", "description", "address", "website", "phone_number", "price_range", "tags", "images", "rating", "llm_interaction_id"}).
				AddRow(hotelID, "Le Meurice", 48.8651, 2.3281, strPtr("Luxury"), strPtr("5-star hotel"), strPtr("228 Rue de Rivoli"), strPtr("https://www.lemeurice.com"), strPtr("+33144581010"), strPtr("$$$"), []string{"luxury", "spa"}, []string{"hotel1.jpg"}, 4.8, llmInteractionID),
		)

	repo := NewRepository(mock, slog.Default())
	hotels, err := repo.GetCityHotelsByInteraction(context.Background(), userID, cityName)
	if err != nil {
		t.Fatalf("GetCityHotelsByInteraction: %v", err)
	}

	if len(hotels) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(hotels))
	}
	if hotels[0].ID != hotelID {
		t.Errorf("expected hotel ID %s, got %s", hotelID, hotels[0].ID)
	}
	if hotels[0].Name != "Le Meurice" {
		t.Errorf("expected hotel name 'Le Meurice', got %s", hotels[0].Name)
	}
	if hotels[0].City != cityName {
		t.Errorf("expected city %s, got %s", cityName, hotels[0].City)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetCityRestaurantsByInteraction(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	cityName := "Paris"
	restaurantID := uuid.New()
	llmInteractionID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT
			rd.id,
			rd.name,
			rd.latitude,
			rd.longitude,
			rd.category,
			rd.description,
			rd.address,
			rd.website,
			rd.phone_number,
			rd.price_level,
			rd.cuisine_type,
			rd.tags,
			rd.images,
			rd.rating,
			rd.llm_interaction_id
		FROM restaurant_details rd
		JOIN llm_interactions li ON rd.llm_interaction_id = li.id
		WHERE li.user_id = $1 AND li.city_name = $2
		ORDER BY rd.created_at DESC
	`)).
		WithArgs(userID, cityName).
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "name", "latitude", "longitude", "category", "description", "address", "website", "phone_number", "price_level", "cuisine_type", "tags", "images", "rating", "llm_interaction_id"}).
				AddRow(restaurantID, "Le Cinq", 48.8696, 2.3005, strPtr("Fine Dining"), strPtr("Michelin starred"), strPtr("31 Avenue George V"), strPtr("https://www.lecinq.com"), strPtr("+33149527177"), strPtr("$$$$"), strPtr("French"), []string{"fine-dining", "michelin"}, []string{"resto1.jpg"}, 4.9, llmInteractionID),
		)

	repo := NewRepository(mock, slog.Default())
	restaurants, err := repo.GetCityRestaurantsByInteraction(context.Background(), userID, cityName)
	if err != nil {
		t.Fatalf("GetCityRestaurantsByInteraction: %v", err)
	}

	if len(restaurants) != 1 {
		t.Fatalf("expected 1 restaurant, got %d", len(restaurants))
	}
	if restaurants[0].ID != restaurantID {
		t.Errorf("expected restaurant ID %s, got %s", restaurantID, restaurants[0].ID)
	}
	if restaurants[0].Name != "Le Cinq" {
		t.Errorf("expected restaurant name 'Le Cinq', got %s", restaurants[0].Name)
	}
	if *restaurants[0].CuisineType != "French" {
		t.Errorf("expected cuisine type 'French', got %s", *restaurants[0].CuisineType)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetCityItinerariesByInteraction(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	cityName := "Paris"
	itineraryID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT
			usi.id,
			usi.user_id,
			usi.source_llm_interaction_id,
			usi.session_id,
			usi.primary_city_id,
			usi.title,
			usi.description,
			usi.markdown_content,
			usi.tags,
			usi.estimated_duration_days,
			usi.estimated_cost_level,
			usi.is_public,
			usi.created_at,
			usi.updated_at
		FROM user_saved_itineraries usi
		JOIN llm_interactions li ON (usi.source_llm_interaction_id = li.id OR usi.session_id IN (
			SELECT DISTINCT session_id FROM llm_interactions WHERE user_id = $1 AND city_name = $2
		))
		WHERE usi.user_id = $1
		ORDER BY usi.created_at DESC
	`)).
		WithArgs(userID, cityName).
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "user_id", "source_llm_interaction_id", "session_id", "primary_city_id", "title", "description", "markdown_content", "tags", "estimated_duration_days", "estimated_cost_level", "is_public", "created_at", "updated_at"}).
				AddRow(itineraryID, userID, nil, nil, nil, "3 Days in Paris", strPtr("A romantic getaway"), "# Day 1\n...", []string{"romantic", "weekend"}, int32Ptr(3), int32Ptr(2), true, now, now),
		)

	repo := NewRepository(mock, slog.Default())
	itineraries, err := repo.GetCityItinerariesByInteraction(context.Background(), userID, cityName)
	if err != nil {
		t.Fatalf("GetCityItinerariesByInteraction: %v", err)
	}

	if len(itineraries) != 1 {
		t.Fatalf("expected 1 itinerary, got %d", len(itineraries))
	}
	if itineraries[0].ID != itineraryID {
		t.Errorf("expected itinerary ID %s, got %s", itineraryID, itineraries[0].ID)
	}
	if itineraries[0].Title != "3 Days in Paris" {
		t.Errorf("expected title '3 Days in Paris', got %s", itineraries[0].Title)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetCityFavorites(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	cityName := "Paris"
	now := time.Now()
	poiID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		-- Regular POI favorites
		SELECT DISTINCT
			pd.id,
			pd.name,
			pd.latitude,
			pd.longitude,
			pd.description,
			pd.address,
			pd.website,
			pd.phone_number,
			pd.opening_hours,
			pd.price_range,
			pd.category,
			pd.tags,
			pd.images,
			pd.rating,
			pd.created_at
		FROM poi_details pd
		JOIN user_favorite_pois ufp ON pd.id = ufp.poi_id
		JOIN cities c ON pd.city_id = c.id
		WHERE ufp.user_id = $1 AND LOWER(c.name) = LOWER($2)

		UNION

		-- LLM POI favorites
		SELECT DISTINCT
			lp.id,
			lp.name,
			lp.latitude,
			lp.longitude,
			lp.description,
			lp.address,
			lp.website,
			lp.phone_number,
			lp.opening_hours,
			lp.price_range,
			lp.category,
			lp.tags,
			lp.images,
			lp.rating,
			lp.created_at
		FROM llm_poi lp
		JOIN user_favorite_llm_pois uflp ON lp.id = uflp.llm_poi_id
		JOIN llm_interactions li ON lp.llm_interaction_id = li.id
		WHERE uflp.user_id = $1 AND LOWER(li.city_name) = LOWER($2)

		ORDER BY created_at DESC
	`)).
		WithArgs(userID, cityName).
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "name", "latitude", "longitude", "description", "address", "website", "phone_number", "opening_hours", "price_range", "category", "tags", "images", "rating", "created_at"}).
				AddRow(poiID, "Louvre Museum", 48.8606, 2.3376, strPtr("World's largest art museum"), strPtr("Rue de Rivoli"), strPtr("https://www.louvre.fr"), strPtr("+33140205050"), strPtr("9:00-18:00"), strPtr("$$"), strPtr("museum"), []string{"art", "museum"}, []string{"louvre.jpg"}, 4.7, now),
		)

	repo := NewRepository(mock, slog.Default())
	favorites, err := repo.GetCityFavorites(context.Background(), userID, cityName)
	if err != nil {
		t.Fatalf("GetCityFavorites: %v", err)
	}

	if len(favorites) != 1 {
		t.Fatalf("expected 1 favorite, got %d", len(favorites))
	}
	if favorites[0].ID != poiID {
		t.Errorf("expected POI ID %s, got %s", poiID, favorites[0].ID)
	}
	if favorites[0].Name != "Louvre Museum" {
		t.Errorf("expected POI name 'Louvre Museum', got %s", favorites[0].Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetUserRecentInteractions(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	sessionID := uuid.New()
	now := time.Now()
	interactionID := uuid.New()

	filterOptions := &locitypes.RecentInteractionsFilter{
		SortBy:          "last_activity",
		SortOrder:       "desc",
		Search:          "",
		MinInteractions: 0,
		MaxInteractions: -1,
	}

	// Expect main query for city interactions
	mock.ExpectQuery(`SELECT\s+city_name`).
		WithArgs(userID, 10, 0).
		WillReturnRows(
			pgxmock.NewRows([]string{"city_name", "last_activity", "interaction_count", "session_id", "title", "poi_count"}).
				AddRow("Paris", now, 5, sessionID, "Trip to Paris", 10),
		)

	// Expect getCityInteractions sub-query
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id,
			user_id,
			city_name,
			city_id,
			prompt,
			response,
			model_name,
			latency_ms,
			created_at
		FROM llm_interactions
		WHERE user_id = $1 AND city_name = $2
		ORDER BY created_at DESC
		LIMIT 5
	`)).
		WithArgs(userID, "Paris").
		WillReturnRows(
			pgxmock.NewRows([]string{"id", "user_id", "city_name", "city_id", "prompt", "response", "model_name", "latency_ms", "created_at"}).
				AddRow(interactionID, userID, "Paris", nil, "What to do in Paris?", strPtr("Visit the Eiffel Tower"), "gpt-4", 150, now),
		)

	// Expect count query
	mock.ExpectQuery(`SELECT COUNT\(\*\) as count FROM`).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{"count"}).AddRow(1),
		)

	repo := NewRepository(mock, slog.Default())
	response, err := repo.GetUserRecentInteractions(context.Background(), userID, 1, 10, filterOptions)
	if err != nil {
		t.Fatalf("GetUserRecentInteractions: %v", err)
	}

	if response.Total != 1 {
		t.Errorf("expected total 1, got %d", response.Total)
	}
	if len(response.Cities) != 1 {
		t.Fatalf("expected 1 city, got %d", len(response.Cities))
	}
	if response.Cities[0].CityName != "Paris" {
		t.Errorf("expected city name 'Paris', got %s", response.Cities[0].CityName)
	}
	if len(response.Cities[0].Interactions) != 1 {
		t.Errorf("expected 1 interaction, got %d", len(response.Cities[0].Interactions))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
