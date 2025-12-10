package city

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
)

func TestRepoFindCityByNameAndCountry(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	cityID := uuid.New()
	lat := 38.7223
	lon := -9.1393

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        AND ($2 = '' OR country = $2)`)).
		WithArgs("Lisbon", "Portugal").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}).AddRow(cityID, "Lisbon", "Portugal", "Lisboa", "A beautiful city", &lat, &lon))

	repo := NewCityRepository(mock, slog.Default())

	city, err := repo.FindCityByNameAndCountry(context.Background(), "Lisbon", "Portugal")
	if err != nil {
		t.Fatalf("FindCityByNameAndCountry: %v", err)
	}

	if city == nil {
		t.Fatal("expected city, got nil")
	}
	if city.ID != cityID || city.Name != "Lisbon" {
		t.Fatalf("unexpected city: %+v", city)
	}
	if city.CenterLatitude == nil || *city.CenterLatitude != lat {
		t.Fatalf("unexpected latitude: %v", city.CenterLatitude)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoFindCityByNameAndCountry_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        AND ($2 = '' OR country = $2)`)).
		WithArgs("NonExistent", "Country").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}))

	repo := NewCityRepository(mock, slog.Default())

	city, err := repo.FindCityByNameAndCountry(context.Background(), "NonExistent", "Country")
	if err != nil {
		t.Fatalf("FindCityByNameAndCountry: %v", err)
	}

	// Should return nil, nil for not found
	if city != nil {
		t.Fatalf("expected nil, got %+v", city)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoFindCityByNameAndCountry_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        AND ($2 = '' OR country = $2)`)).
		WithArgs("Lisbon", "Portugal").
		WillReturnError(errors.New("database error"))

	repo := NewCityRepository(mock, slog.Default())

	_, err = repo.FindCityByNameAndCountry(context.Background(), "Lisbon", "Portugal")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoFindCityByFuzzyName(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	cityID := uuid.New()
	lat := 41.1579
	lon := -8.6291

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, name, country,
			COALESCE(state_province, '') as state_province,
			COALESCE(ai_summary, '') as ai_summary,
			ST_Y(center_location) as center_latitude,
			ST_X(center_location) as center_longitude
		FROM cities
		WHERE similarity(name, $1) > 0.3
		ORDER BY similarity(name, $1) DESC
		LIMIT 1`)).
		WithArgs("Prto").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}).AddRow(cityID, "Porto", "Portugal", "Porto", "Northern Portugal city", &lat, &lon))

	repo := NewCityRepository(mock, slog.Default())

	city, err := repo.FindCityByFuzzyName(context.Background(), "Prto")
	if err != nil {
		t.Fatalf("FindCityByFuzzyName: %v", err)
	}

	if city == nil {
		t.Fatal("expected city, got nil")
	}
	if city.Name != "Porto" {
		t.Fatalf("unexpected city name: %s", city.Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoFindCityByFuzzyName_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			id, name, country,
			COALESCE(state_province, '') as state_province,
			COALESCE(ai_summary, '') as ai_summary,
			ST_Y(center_location) as center_latitude,
			ST_X(center_location) as center_longitude
		FROM cities
		WHERE similarity(name, $1) > 0.3
		ORDER BY similarity(name, $1) DESC
		LIMIT 1`)).
		WithArgs("zzzzz").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}))

	repo := NewCityRepository(mock, slog.Default())

	city, err := repo.FindCityByFuzzyName(context.Background(), "zzzzz")
	if err != nil {
		t.Fatalf("FindCityByFuzzyName: %v", err)
	}

	if city != nil {
		t.Fatalf("expected nil for no match, got %+v", city)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetCityIDByName(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	cityID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        LIMIT 1`)).
		WithArgs("Lisbon").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(cityID))

	repo := NewCityRepository(mock, slog.Default())

	result, err := repo.GetCityIDByName(context.Background(), "Lisbon")
	if err != nil {
		t.Fatalf("GetCityIDByName: %v", err)
	}

	if result != cityID {
		t.Fatalf("expected %s, got %s", cityID, result)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetCityIDByName_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT id
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        LIMIT 1`)).
		WithArgs("NonExistent").
		WillReturnRows(pgxmock.NewRows([]string{"id"}))

	repo := NewCityRepository(mock, slog.Default())

	result, err := repo.GetCityIDByName(context.Background(), "NonExistent")
	if err == nil {
		t.Fatal("expected error for not found, got nil")
	}
	if result != uuid.Nil {
		t.Fatalf("expected uuid.Nil, got %s", result)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetAllCities(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	cityID1 := uuid.New()
	cityID2 := uuid.New()
	lat1 := 38.7223
	lon1 := -9.1393
	lat2 := 41.1579
	lon2 := -8.6291

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id,
            name,
            country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        ORDER BY name ASC`)).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}).
			AddRow(cityID1, "Lisbon", "Portugal", "Lisboa", "Capital city", &lat1, &lon1).
			AddRow(cityID2, "Porto", "Portugal", "Porto", "Northern city", &lat2, &lon2))

	repo := NewCityRepository(mock, slog.Default())

	cities, err := repo.GetAllCities(context.Background())
	if err != nil {
		t.Fatalf("GetAllCities: %v", err)
	}

	if len(cities) != 2 {
		t.Fatalf("expected 2 cities, got %d", len(cities))
	}
	if cities[0].Name != "Lisbon" || cities[1].Name != "Porto" {
		t.Fatalf("unexpected cities: %+v", cities)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetAllCities_Empty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id,
            name,
            country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        ORDER BY name ASC`)).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}))

	repo := NewCityRepository(mock, slog.Default())

	cities, err := repo.GetAllCities(context.Background())
	if err != nil {
		t.Fatalf("GetAllCities: %v", err)
	}

	if len(cities) != 0 {
		t.Fatalf("expected 0 cities, got %d", len(cities))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetAllCities_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id,
            name,
            country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        ORDER BY name ASC`)).
		WillReturnError(errors.New("database error"))

	repo := NewCityRepository(mock, slog.Default())

	_, err = repo.GetAllCities(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoGetCitiesWithoutEmbeddings(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	cityID := uuid.New()
	lat := 38.7223
	lon := -9.1393

	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id,
            name,
            country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE embedding IS NULL
        ORDER BY created_at ASC
        LIMIT $1`)).
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}).AddRow(cityID, "NewCity", "Portugal", "", "No summary yet", &lat, &lon))

	repo := NewCityRepository(mock, slog.Default())

	cities, err := repo.GetCitiesWithoutEmbeddings(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetCitiesWithoutEmbeddings: %v", err)
	}

	if len(cities) != 1 {
		t.Fatalf("expected 1 city, got %d", len(cities))
	}
	if cities[0].Name != "NewCity" {
		t.Fatalf("unexpected city: %+v", cities[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoSaveCity_NewCity(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	newCityID := uuid.New()
	lat := 40.4168
	lon := -3.7038

	// First query for FindCityByNameAndCountry - returns empty (city doesn't exist)
	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        AND ($2 = '' OR country = $2)`)).
		WithArgs("Madrid", "Spain").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}))

	// Insert query
	mock.ExpectQuery(regexp.QuoteMeta(`
        INSERT INTO cities (
            name, country, state_province, ai_summary, center_location
        ) VALUES (
            $1, $2, $3, $4,
            CASE
                WHEN ($5::DOUBLE PRECISION IS NOT NULL AND $6::DOUBLE PRECISION IS NOT NULL)
                     AND ($5::DOUBLE PRECISION != 0.0 OR $6::DOUBLE PRECISION != 0.0)
                     AND ($5::DOUBLE PRECISION >= -180 AND $5::DOUBLE PRECISION <= 180)
                     AND ($6::DOUBLE PRECISION >= -90 AND $6::DOUBLE PRECISION <= 90)
                THEN ST_SetSRID(ST_MakePoint($5::DOUBLE PRECISION, $6::DOUBLE PRECISION), 4326)
                ELSE NULL
            END
        )
        ON CONFLICT (name, state_province, country)
        DO UPDATE SET
            ai_summary = COALESCE(EXCLUDED.ai_summary, cities.ai_summary),
            center_location = COALESCE(EXCLUDED.center_location, cities.center_location),
            updated_at = NOW()
        RETURNING id`)).
		WithArgs("Madrid", "Spain", "Unknown", "Capital of Spain", &lon, &lat).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(newCityID))

	repo := NewCityRepository(mock, slog.Default())

	city := locitypes.CityDetail{
		Name:            "Madrid",
		Country:         "Spain",
		AiSummary:       "Capital of Spain",
		CenterLatitude:  &lat,
		CenterLongitude: &lon,
	}

	result, err := repo.SaveCity(context.Background(), city)
	if err != nil {
		t.Fatalf("SaveCity: %v", err)
	}

	if result != newCityID {
		t.Fatalf("expected %s, got %s", newCityID, result)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRepoSaveCity_ExistingCity(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	existingCityID := uuid.New()
	lat := 38.7223
	lon := -9.1393

	// First query for FindCityByNameAndCountry - returns existing city
	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT
            id, name, country,
            COALESCE(state_province, '') as state_province,
            COALESCE(ai_summary, '') as ai_summary,
            ST_Y(center_location) as center_latitude,
            ST_X(center_location) as center_longitude
        FROM cities
        WHERE LOWER(name) = LOWER($1)
        AND ($2 = '' OR country = $2)`)).
		WithArgs("Lisbon", "Portugal").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "country", "state_province", "ai_summary", "center_latitude", "center_longitude",
		}).AddRow(existingCityID, "Lisbon", "Portugal", "Lisboa", "Existing city", &lat, &lon))

	repo := NewCityRepository(mock, slog.Default())

	city := locitypes.CityDetail{
		Name:            "Lisbon",
		Country:         "Portugal",
		AiSummary:       "Duplicate city",
		CenterLatitude:  &lat,
		CenterLongitude: &lon,
	}

	result, err := repo.SaveCity(context.Background(), city)
	if err != nil {
		t.Fatalf("SaveCity: %v", err)
	}

	// Should return existing city ID
	if result != existingCityID {
		t.Fatalf("expected existing city ID %s, got %s", existingCityID, result)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
