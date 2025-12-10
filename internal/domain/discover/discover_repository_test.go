package discover

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
)

func TestRepoGetTrendingDiscoveries(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			city_name,
			COUNT(*) as search_count,
			MAX(created_at) as last_search
		FROM chat_sessions
		WHERE
			created_at >= NOW() - INTERVAL '7 days'
			AND city_name IS NOT NULL
			AND city_name != ''
		GROUP BY city_name
		ORDER BY search_count DESC, last_search DESC
		LIMIT $1`)).
		WithArgs(5).
		WillReturnRows(pgxmock.NewRows([]string{"city_name", "search_count", "last_search"}).
			AddRow("Lisbon", 25, now.Add(-1*time.Hour)).
			AddRow("Porto", 18, now.Add(-2*time.Hour)))

	repo := NewRepositoryImpl(mock, slog.Default())

	discoveries, err := repo.GetTrendingDiscoveries(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetTrendingDiscoveries: %v", err)
	}

	if len(discoveries) != 2 {
		t.Fatalf("expected 2 discoveries, got %d", len(discoveries))
	}
	if discoveries[0].CityName != "Lisbon" || discoveries[0].SearchCount != 25 {
		t.Fatalf("unexpected first discovery: %+v", discoveries[0])
	}
	if discoveries[1].CityName != "Porto" || discoveries[1].SearchCount != 18 {
		t.Fatalf("unexpected second discovery: %+v", discoveries[1])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetTrendingDiscoveries_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			city_name,
			COUNT(*) as search_count,
			MAX(created_at) as last_search
		FROM chat_sessions
		WHERE
			created_at >= NOW() - INTERVAL '7 days'
			AND city_name IS NOT NULL
			AND city_name != ''
		GROUP BY city_name
		ORDER BY search_count DESC, last_search DESC
		LIMIT $1`)).
		WithArgs(5).
		WillReturnError(errors.New("database connection error"))

	repo := NewRepositoryImpl(mock, slog.Default())

	_, err = repo.GetTrendingDiscoveries(context.Background(), 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetFeaturedCollections(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			category,
			COUNT(*) as item_count,
			MAX(created_at) as last_updated
		FROM poi_detailed_info
		WHERE
			rating >= 4.0
			AND category IS NOT NULL
			AND category != ''
		GROUP BY category
		ORDER BY item_count DESC
		LIMIT $1`)).
		WithArgs(6).
		WillReturnRows(pgxmock.NewRows([]string{"category", "item_count", "last_updated"}).
			AddRow("restaurant", 150, now.Add(-1*time.Hour)).
			AddRow("hotel", 80, now.Add(-2*time.Hour)))

	repo := NewRepositoryImpl(mock, slog.Default())

	collections, err := repo.GetFeaturedCollections(context.Background(), 6)
	if err != nil {
		t.Fatalf("GetFeaturedCollections: %v", err)
	}

	if len(collections) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(collections))
	}
	if collections[0].Category != "restaurant" || collections[0].ItemCount != 150 {
		t.Fatalf("unexpected first collection: %+v", collections[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetFeaturedCollections_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			category,
			COUNT(*) as item_count,
			MAX(created_at) as last_updated
		FROM poi_detailed_info
		WHERE
			rating >= 4.0
			AND category IS NOT NULL
			AND category != ''
		GROUP BY category
		ORDER BY item_count DESC
		LIMIT $1`)).
		WithArgs(6).
		WillReturnError(errors.New("query failed"))

	repo := NewRepositoryImpl(mock, slog.Default())

	_, err = repo.GetFeaturedCollections(context.Background(), 6)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetRecentDiscoveriesByUserID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	sessionID := uuid.New()
	profileID := uuid.New()
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	// Expect count query (actual query in repo)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) as count FROM chat_sessions WHERE user_id = $1`)).
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(10))

	// Expect sessions query
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id,
			user_id,
			profile_id,
			city_name,
			conversation_history,
			session_context,
			created_at,
			updated_at,
			expires_at,
			status
		FROM chat_sessions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`)).
		WithArgs(userID, 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "user_id", "profile_id", "city_name", "conversation_history",
			"session_context", "created_at", "updated_at", "expires_at", "status",
		}).AddRow(sessionID, userID, &profileID, "Lisbon", []byte("[]"), []byte("{}"), now, now, expiresAt, "active"))

	repo := NewRepositoryImpl(mock, slog.Default())

	sessions, total, err := repo.GetRecentDiscoveriesByUserID(context.Background(), userID, 10, 0)
	if err != nil {
		t.Fatalf("GetRecentDiscoveriesByUserID: %v", err)
	}

	if total != 10 {
		t.Fatalf("expected total 10, got %d", total)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != sessionID || sessions[0].CityName != "Lisbon" {
		t.Fatalf("unexpected session: %+v", sessions[0])
	}
	if sessions[0].ProfileID != profileID {
		t.Fatalf("expected profile ID %s, got %s", profileID, sessions[0].ProfileID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetRecentDiscoveriesByUserID_CountError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) as count FROM chat_sessions WHERE user_id = $1`)).
		WithArgs(userID).
		WillReturnError(errors.New("count query failed"))

	repo := NewRepositoryImpl(mock, slog.Default())

	_, _, err = repo.GetRecentDiscoveriesByUserID(context.Background(), userID, 10, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetRecentDiscoveriesByUserID_NilProfileID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()
	sessionID := uuid.New()
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	// Expect count query
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) as count FROM chat_sessions WHERE user_id = $1`)).
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// Expect sessions query with nil profile_id
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id,
			user_id,
			profile_id,
			city_name,
			conversation_history,
			session_context,
			created_at,
			updated_at,
			expires_at,
			status
		FROM chat_sessions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`)).
		WithArgs(userID, 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "user_id", "profile_id", "city_name", "conversation_history",
			"session_context", "created_at", "updated_at", "expires_at", "status",
		}).AddRow(sessionID, userID, nil, "Porto", []byte("[]"), []byte("{}"), now, now, expiresAt, "active"))

	repo := NewRepositoryImpl(mock, slog.Default())

	sessions, total, err := repo.GetRecentDiscoveriesByUserID(context.Background(), userID, 10, 0)
	if err != nil {
		t.Fatalf("GetRecentDiscoveriesByUserID: %v", err)
	}

	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	// ProfileID should be zero value UUID when nil
	if sessions[0].ProfileID != uuid.Nil {
		t.Fatalf("expected nil profile ID, got %s", sessions[0].ProfileID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetPOIsByCategory(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	poiID := uuid.New()
	website := "https://example.com"
	phone := "+1234567890"

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id,
			name,
			latitude,
			longitude,
			category,
			description_poi,
			address,
			website,
			phone_number,
			price_level,
			rating,
			tags
		FROM poi_detailed_info
		WHERE
			LOWER(category) = LOWER($1)
			AND rating >= 3.5
		
		ORDER BY rating DESC, name ASC
		LIMIT $2 OFFSET $3`)).
		WithArgs("restaurant", 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "latitude", "longitude", "category", "description_poi",
			"address", "website", "phone_number", "price_level", "rating", "tags",
		}).AddRow(
			poiID, "Test Restaurant", 38.7223, -9.1393, "restaurant", "A great restaurant",
			"123 Main St", &website, &phone, "$$", 4.5, []string{"food", "dinner"},
		))

	repo := NewRepositoryImpl(mock, slog.Default())

	results, err := repo.GetPOIsByCategory(context.Background(), "restaurant", "", 10, 0)
	if err != nil {
		t.Fatalf("GetPOIsByCategory: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Test Restaurant" || results[0].Rating != 4.5 {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	if results[0].Website == nil || *results[0].Website != website {
		t.Fatalf("unexpected website: %v", results[0].Website)
	}
	if results[0].PhoneNumber == nil || *results[0].PhoneNumber != phone {
		t.Fatalf("unexpected phone: %v", results[0].PhoneNumber)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetPOIsByCategory_WithCityFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	poiID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id,
			name,
			latitude,
			longitude,
			category,
			description_poi,
			address,
			website,
			phone_number,
			price_level,
			rating,
			tags
		FROM poi_detailed_info
		WHERE
			LOWER(category) = LOWER($1)
			AND rating >= 3.5
		
			AND LOWER(city_name) = LOWER($2)
		
		ORDER BY rating DESC, name ASC
		LIMIT $3 OFFSET $4`)).
		WithArgs("hotel", "Lisbon", 5, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "latitude", "longitude", "category", "description_poi",
			"address", "website", "phone_number", "price_level", "rating", "tags",
		}).AddRow(
			poiID, "Lisbon Hotel", 38.7223, -9.1393, "hotel", "A great hotel",
			"456 Hotel St", nil, nil, "$$$", 4.8, []string{"luxury", "pool"},
		))

	repo := NewRepositoryImpl(mock, slog.Default())

	results, err := repo.GetPOIsByCategory(context.Background(), "hotel", "Lisbon", 5, 0)
	if err != nil {
		t.Fatalf("GetPOIsByCategory with city: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Lisbon Hotel" {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	// Check nullable fields are nil
	if results[0].Website != nil {
		t.Fatalf("expected nil website, got %v", results[0].Website)
	}
	if results[0].PhoneNumber != nil {
		t.Fatalf("expected nil phone, got %v", results[0].PhoneNumber)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetPOIsByCategory_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id,
			name,
			latitude,
			longitude,
			category,
			description_poi,
			address,
			website,
			phone_number,
			price_level,
			rating,
			tags
		FROM poi_detailed_info
		WHERE
			LOWER(category) = LOWER($1)
			AND rating >= 3.5
		
		ORDER BY rating DESC, name ASC
		LIMIT $2 OFFSET $3`)).
		WithArgs("restaurant", 10, 0).
		WillReturnError(errors.New("query failed"))

	repo := NewRepositoryImpl(mock, slog.Default())

	_, err = repo.GetPOIsByCategory(context.Background(), "restaurant", "", 10, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetTrendingSearchesToday(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			query,
			city_name,
			COUNT(*) as search_count,
			MAX(created_at) as last_searched
		FROM discover_searches
		WHERE
			created_at >= NOW() - INTERVAL '24 hours'
			AND query IS NOT NULL
			AND query != ''
			AND city_name IS NOT NULL
			AND city_name != ''
		GROUP BY query, city_name
		ORDER BY search_count DESC, last_searched DESC
		LIMIT $1`)).
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"query", "city_name", "search_count", "last_searched"}).
			AddRow("best restaurants", "Lisbon", 50, now.Add(-30*time.Minute)).
			AddRow("museums", "Porto", 35, now.Add(-1*time.Hour)))

	repo := NewRepositoryImpl(mock, slog.Default())

	searches, err := repo.GetTrendingSearchesToday(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetTrendingSearchesToday: %v", err)
	}

	if len(searches) != 2 {
		t.Fatalf("expected 2 searches, got %d", len(searches))
	}
	if searches[0].Query != "best restaurants" || searches[0].CityName != "Lisbon" {
		t.Fatalf("unexpected first search: %+v", searches[0])
	}
	if searches[0].SearchCount != 50 {
		t.Fatalf("expected search count 50, got %d", searches[0].SearchCount)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetTrendingSearchesToday_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			query,
			city_name,
			COUNT(*) as search_count,
			MAX(created_at) as last_searched
		FROM discover_searches
		WHERE
			created_at >= NOW() - INTERVAL '24 hours'
			AND query IS NOT NULL
			AND query != ''
			AND city_name IS NOT NULL
			AND city_name != ''
		GROUP BY query, city_name
		ORDER BY search_count DESC, last_searched DESC
		LIMIT $1`)).
		WithArgs(10).
		WillReturnError(errors.New("database error"))

	repo := NewRepositoryImpl(mock, slog.Default())

	_, err = repo.GetTrendingSearchesToday(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoTrackSearch(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO discover_searches (
			user_id,
			query,
			city_name,
			search_type,
			result_count,
			source,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())`)).
		WithArgs(userID, "best pizza", "Naples", "discover", 15, "mobile").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	repo := NewRepositoryImpl(mock, slog.Default())

	err = repo.TrackSearch(context.Background(), userID, "best pizza", "Naples", "mobile", 15)
	if err != nil {
		t.Fatalf("TrackSearch: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoTrackSearch_ExecError_NoErrorPropagation(t *testing.T) {
	// TrackSearch returns nil even on error (by design - tracking failures shouldn't fail the request)
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	userID := uuid.New()

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO discover_searches (
			user_id,
			query,
			city_name,
			search_type,
			result_count,
			source,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())`)).
		WithArgs(userID, "best pizza", "Naples", "discover", 15, "mobile").
		WillReturnError(errors.New("insert failed"))

	repo := NewRepositoryImpl(mock, slog.Default())

	// TrackSearch intentionally returns nil on error (see repo implementation)
	err = repo.TrackSearch(context.Background(), userID, "best pizza", "Naples", "mobile", 15)
	if err != nil {
		t.Fatalf("TrackSearch should return nil even on error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoTrackSearch_NilUserID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	// When userID is uuid.Nil, it should pass nil to the query
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO discover_searches (
			user_id,
			query,
			city_name,
			search_type,
			result_count,
			source,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())`)).
		WithArgs(nil, "anonymous search", "London", "discover", 5, "web").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	repo := NewRepositoryImpl(mock, slog.Default())

	err = repo.TrackSearch(context.Background(), uuid.Nil, "anonymous search", "London", "web", 5)
	if err != nil {
		t.Fatalf("TrackSearch with nil user: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestFormatTimeAgo(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			name:     "just now",
			input:    time.Now().Add(-30 * time.Second),
			expected: "just now",
		},
		{
			name:     "minutes ago",
			input:    time.Now().Add(-5 * time.Minute),
			expected: "5 minutes ago",
		},
		{
			name:     "hours ago",
			input:    time.Now().Add(-3 * time.Hour),
			expected: "3 hours ago",
		},
		{
			name:     "days ago",
			input:    time.Now().Add(-2 * 24 * time.Hour),
			expected: "2 days ago",
		},
		{
			name:     "1 minute ago",
			input:    time.Now().Add(-1 * time.Minute),
			expected: "1 minute ago",
		},
		{
			name:     "1 hour ago",
			input:    time.Now().Add(-1 * time.Hour),
			expected: "1 hour ago",
		},
		{
			name:     "1 day ago",
			input:    time.Now().Add(-24 * time.Hour),
			expected: "1 day ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTimeAgo(tt.input)
			if result != tt.expected {
				t.Errorf("formatTimeAgo(%v) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}
