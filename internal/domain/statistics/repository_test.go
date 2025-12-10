package statistics

import (
	"context"
	"log/slog"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
)

func TestGetMainPageStatistics(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
			WITH user_unique_pois AS (
				-- POIs from poi_details
				SELECT DISTINCT
					pd.name,
					pd.latitude,
					pd.longitude,
					'poi_details' as source_table
				FROM poi_details pd
				JOIN llm_interactions li ON pd.llm_interaction_id = li.id
				WHERE li.user_id = $1

				UNION

				-- POIs from llm_suggested_pois
				SELECT DISTINCT
					lsp.name,
					lsp.latitude,
					lsp.longitude,
					'llm_suggested_pois' as source_table
				FROM llm_suggested_pois lsp
				WHERE lsp.user_id = $1

				UNION

				-- Hotels
				SELECT DISTINCT
					hd.name,
					hd.latitude,
					hd.longitude,
					'hotel_details' as source_table
				FROM hotel_details hd
				JOIN llm_interactions li ON hd.llm_interaction_id = li.id
				WHERE li.user_id = $1

				UNION

				-- Restaurants
				SELECT DISTINCT
					rd.name,
					rd.latitude,
					rd.longitude,
					'restaurant_details' as source_table
				FROM restaurant_details rd
				JOIN llm_interactions li ON rd.llm_interaction_id = li.id
				WHERE li.user_id = $1
			),
			user_itineraries AS (
				-- Count saved/bookmarked itineraries for the user
				SELECT COUNT(*) as saved_itinerary_count
				FROM user_saved_itineraries usi
				WHERE usi.user_id = $1
			),
			total_users AS (
				-- Count total active users in the system
				SELECT COUNT(*) as user_count
				FROM users u
				WHERE u.is_active = true
			)
			SELECT
				(SELECT user_count FROM total_users) AS total_users_count,
				(SELECT saved_itinerary_count FROM user_itineraries) AS total_itineraries_saved,
				COUNT(*) AS total_unique_pois
			FROM user_unique_pois;
		`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{"total_users_count", "total_itineraries_saved", "total_unique_pois"}).
				AddRow(int64(100), int64(50), int64(25)),
		)

	repo := NewRepository(slog.Default(), mock)
	stats, err := repo.GetMainPageStatistics(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetMainPageStatistics: %v", err)
	}

	if stats.TotalUsersCount != 100 {
		t.Errorf("expected TotalUsersCount=100, got %d", stats.TotalUsersCount)
	}
	if stats.TotalItinerariesSaved != 50 {
		t.Errorf("expected TotalItinerariesSaved=50, got %d", stats.TotalItinerariesSaved)
	}
	if stats.TotalUniquePOIs != 25 {
		t.Errorf("expected TotalUniquePOIs=25, got %d", stats.TotalUniquePOIs)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetMainPageStatisticsSystemUser(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	systemUserID := uuid.MustParse("00000000-0000-0000-0000-000000000000")

	mock.ExpectQuery(regexp.QuoteMeta(`
			WITH all_unique_pois AS (
				-- POIs from poi_details (all users)
				SELECT DISTINCT
					pd.name,
					pd.latitude,
					pd.longitude,
					'poi_details' as source_table
				FROM poi_details pd
				JOIN llm_interactions li ON pd.llm_interaction_id = li.id

				UNION

				-- POIs from llm_suggested_pois (all users)
				SELECT DISTINCT
					lsp.name,
					lsp.latitude,
					lsp.longitude,
					'llm_suggested_pois' as source_table
				FROM llm_suggested_pois lsp

				UNION

				-- POIs from hotel_details (all users)
				SELECT DISTINCT
					hd.name,
					hd.latitude,
					hd.longitude,
					'hotel_details' as source_table
				FROM hotel_details hd
				JOIN llm_interactions li ON hd.llm_interaction_id = li.id

				UNION

				-- POIs from restaurant_details (all users)
				SELECT DISTINCT
					rd.name,
					rd.latitude,
					rd.longitude,
					'restaurant_details' as source_table
				FROM restaurant_details rd
				JOIN llm_interactions li ON rd.llm_interaction_id = li.id
			),
			total_itineraries AS (
				-- Count all saved itineraries across all users
				SELECT COUNT(*) as itinerary_count
				FROM user_saved_itineraries usi
			),
			total_users AS (
				-- Count total active users in the system
				SELECT COUNT(*) as user_count
				FROM users u
				WHERE u.is_active = true
			)
			SELECT
				(SELECT user_count FROM total_users) AS total_users_count,
				(SELECT itinerary_count FROM total_itineraries) AS total_itineraries_saved,
				COUNT(*) AS total_unique_pois
			FROM all_unique_pois;
		`)).
		WillReturnRows(
			pgxmock.NewRows([]string{"total_users_count", "total_itineraries_saved", "total_unique_pois"}).
				AddRow(int64(500), int64(200), int64(1000)),
		)

	repo := NewRepository(slog.Default(), mock)
	stats, err := repo.GetMainPageStatistics(context.Background(), systemUserID)
	if err != nil {
		t.Fatalf("GetMainPageStatistics (system user): %v", err)
	}

	if stats.TotalUsersCount != 500 {
		t.Errorf("expected TotalUsersCount=500, got %d", stats.TotalUsersCount)
	}
	if stats.TotalItinerariesSaved != 200 {
		t.Errorf("expected TotalItinerariesSaved=200, got %d", stats.TotalItinerariesSaved)
	}
	if stats.TotalUniquePOIs != 1000 {
		t.Errorf("expected TotalUniquePOIs=1000, got %d", stats.TotalUniquePOIs)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetDetailedPOIStatistics(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(DISTINCT pd.id) as general_pois,
			COUNT(DISTINCT lsp.id) as suggested_pois,
			COUNT(DISTINCT hd.id) as hotels,
			COUNT(DISTINCT rd.id) as restaurants,
			(COUNT(DISTINCT pd.id) + COUNT(DISTINCT lsp.id) + COUNT(DISTINCT hd.id) + COUNT(DISTINCT rd.id)) as total_pois
		FROM llm_interactions li
		LEFT JOIN poi_details pd ON li.id = pd.llm_interaction_id AND li.user_id = $1
		LEFT JOIN llm_suggested_pois lsp ON li.user_id = lsp.user_id
		LEFT JOIN hotel_details hd ON li.id = hd.llm_interaction_id AND li.user_id = $1
		LEFT JOIN restaurant_details rd ON li.id = rd.llm_interaction_id AND li.user_id = $1
		WHERE li.user_id = $1
	`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{"general_pois", "suggested_pois", "hotels", "restaurants", "total_pois"}).
				AddRow(int64(10), int64(20), int64(5), int64(15), int64(50)),
		)

	repo := NewRepository(slog.Default(), mock)
	stats, err := repo.GetDetailedPOIStatistics(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetDetailedPOIStatistics: %v", err)
	}

	if stats.GeneralPOIs != 10 {
		t.Errorf("expected GeneralPOIs=10, got %d", stats.GeneralPOIs)
	}
	if stats.SuggestedPOIs != 20 {
		t.Errorf("expected SuggestedPOIs=20, got %d", stats.SuggestedPOIs)
	}
	if stats.Hotels != 5 {
		t.Errorf("expected Hotels=5, got %d", stats.Hotels)
	}
	if stats.Restaurants != 15 {
		t.Errorf("expected Restaurants=15, got %d", stats.Restaurants)
	}
	if stats.TotalPOIs != 50 {
		t.Errorf("expected TotalPOIs=50, got %d", stats.TotalPOIs)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestLandingPageStatistics(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
	SELECT
    (SELECT COUNT(*) FROM user_favorite_llm_pois WHERE user_id = $1) AS saved_places,
    (SELECT COUNT(*) FROM itineraries WHERE user_id = $1) AS itineraries,
    (SELECT COUNT(DISTINCT city_id) FROM itineraries WHERE user_id = $1) AS cities_explored,
    (SELECT COUNT(*) FROM chat_sessions WHERE user_id = $1) AS discoveries;
	`)).
		WithArgs(userID).
		WillReturnRows(
			pgxmock.NewRows([]string{"saved_places", "itineraries", "cities_explored", "discoveries"}).
				AddRow(int64(10), int64(5), int64(3), int64(20)),
		)

	repo := NewRepository(slog.Default(), mock)
	stats, err := repo.LandingPageStatistics(context.Background(), userID)
	if err != nil {
		t.Fatalf("LandingPageStatistics: %v", err)
	}

	if stats.SavedPlaces != 10 {
		t.Errorf("expected SavedPlaces=10, got %d", stats.SavedPlaces)
	}
	if stats.Itineraries != 5 {
		t.Errorf("expected Itineraries=5, got %d", stats.Itineraries)
	}
	if stats.CitiesExplored != 3 {
		t.Errorf("expected CitiesExplored=3, got %d", stats.CitiesExplored)
	}
	if stats.Discoveries != 20 {
		t.Errorf("expected Discoveries=20, got %d", stats.Discoveries)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
