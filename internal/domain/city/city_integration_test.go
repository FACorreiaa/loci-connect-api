//go:build integration

package city

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testCityDB   *pgxpool.Pool
	testCityRepo Repository
)

func f64(v float64) *float64 { return &v }

func TestMain(m *testing.M) {
	testCityDB = testsupport.MustPool()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	testCityRepo = NewCityRepository(testCityDB, logger)
	os.Exit(m.Run())
}

func clearCityTables(t *testing.T) {
	t.Helper()
	_, err := testCityDB.Exec(context.Background(), "DELETE FROM cities WHERE name LIKE '%TestCity%'")
	require.NoError(t, err, "Failed to clear test cities")
}

func TestCityRepository_SaveCity_Integration(t *testing.T) {
	ctx := context.Background()
	clearCityTables(t)

	t.Run("Save city with coordinates", func(t *testing.T) {
		city := locitypes.CityDetail{
			Name:            "TestCity1",
			Country:         "TestCountry",
			StateProvince:   "TestState",
			AiSummary:       "A beautiful test city with amazing attractions",
			CenterLatitude:  f64(38.7223),
			CenterLongitude: f64(-9.1393),
		}

		cityID, err := testCityRepo.SaveCity(ctx, city)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, cityID)

		// Verify in database
		var dbName, dbCountry, dbState, dbSummary string
		var dbLat, dbLon float64
		err = testCityDB.QueryRow(ctx,
			"SELECT name, country, state_province, ai_summary, ST_Y(center_location), ST_X(center_location) FROM cities WHERE id = $1",
			cityID).Scan(&dbName, &dbCountry, &dbState, &dbSummary, &dbLat, &dbLon)
		require.NoError(t, err)

		assert.Equal(t, city.Name, dbName)
		assert.Equal(t, city.Country, dbCountry)
		assert.Equal(t, city.StateProvince, dbState)
		assert.Equal(t, city.AiSummary, dbSummary)
		assert.InDelta(t, *city.CenterLatitude, dbLat, 0.0001)
		assert.InDelta(t, *city.CenterLongitude, dbLon, 0.0001)
	})

	t.Run("Save city without coordinates", func(t *testing.T) {
		city := locitypes.CityDetail{
			Name:          "TestCity2",
			Country:       "TestCountry",
			StateProvince: "TestState",
			AiSummary:     "Another test city without coordinates",
			// No coordinates provided (nil -> NULL location)
		}

		cityID, err := testCityRepo.SaveCity(ctx, city)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, cityID)

		// Verify in database - center_location should be NULL
		var dbName string
		var hasLocation bool
		err = testCityDB.QueryRow(ctx,
			"SELECT name, center_location IS NOT NULL FROM cities WHERE id = $1",
			cityID).Scan(&dbName, &hasLocation)
		require.NoError(t, err)

		assert.Equal(t, city.Name, dbName)
		assert.False(t, hasLocation, "center_location should be NULL when no coordinates provided")
	})

	t.Run("Save city with empty state province", func(t *testing.T) {
		city := locitypes.CityDetail{
			Name:            "TestCity4",
			Country:         "TestCountry",
			StateProvince:   "", // Empty state province
			AiSummary:       "City without state province",
			CenterLatitude:  f64(40.7128),
			CenterLongitude: f64(-74.0060),
		}

		cityID, err := testCityRepo.SaveCity(ctx, city)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, cityID)

		// Verify in database
		var dbStateProvince string
		var stateIsNull bool
		err = testCityDB.QueryRow(ctx,
			"SELECT COALESCE(state_province, ''), state_province IS NULL FROM cities WHERE id = $1",
			cityID).Scan(&dbStateProvince, &stateIsNull)
		require.NoError(t, err)

		// SaveCity normalizes an empty StateProvince to the sentinel "Unknown"
		// (a consistent default rather than NULL).
		assert.Equal(t, "Unknown", dbStateProvince)
		assert.False(t, stateIsNull, "state_province defaults to 'Unknown' when empty")
	})
}

func TestCityRepository_FindCityByNameAndCountry_Integration(t *testing.T) {
	ctx := context.Background()
	clearCityTables(t)

	// Setup test cities
	testCities := []locitypes.CityDetail{
		{
			Name:            "TestCityFind1",
			Country:         "Portugal",
			StateProvince:   "Lisbon",
			AiSummary:       "Capital city of Portugal",
			CenterLatitude:  f64(38.7223),
			CenterLongitude: f64(-9.1393),
		},
		{
			Name:            "TestCityFind2",
			Country:         "Spain",
			StateProvince:   "Madrid",
			AiSummary:       "Capital city of Spain",
			CenterLatitude:  f64(40.4168),
			CenterLongitude: f64(-3.7038),
		},
		{
			Name:            "TestCityFind3",
			Country:         "Portugal",
			StateProvince:   "Porto",
			AiSummary:       "Second largest city in Portugal",
			CenterLatitude:  f64(41.1579),
			CenterLongitude: f64(-8.6291),
		},
	}

	// Save test cities
	var savedIDs []uuid.UUID
	for _, city := range testCities {
		id, err := testCityRepo.SaveCity(ctx, city)
		require.NoError(t, err)
		savedIDs = append(savedIDs, id)
	}

	t.Run("Find existing city by name and country", func(t *testing.T) {
		foundCity, err := testCityRepo.FindCityByNameAndCountry(ctx, "TestCityFind1", "Portugal")
		require.NoError(t, err)
		require.NotNil(t, foundCity)

		assert.Equal(t, testCities[0].Name, foundCity.Name)
		assert.Equal(t, testCities[0].Country, foundCity.Country)
		assert.Equal(t, testCities[0].StateProvince, foundCity.StateProvince)
		assert.Equal(t, testCities[0].AiSummary, foundCity.AiSummary)
		require.NotNil(t, foundCity.CenterLatitude)
		require.NotNil(t, foundCity.CenterLongitude)
		assert.InDelta(t, *testCities[0].CenterLatitude, *foundCity.CenterLatitude, 0.0001)
		assert.InDelta(t, *testCities[0].CenterLongitude, *foundCity.CenterLongitude, 0.0001)
		assert.Equal(t, savedIDs[0], foundCity.ID)
	})

	t.Run("Find city by name only (empty country)", func(t *testing.T) {
		foundCity, err := testCityRepo.FindCityByNameAndCountry(ctx, "TestCityFind2", "")
		require.NoError(t, err)
		require.NotNil(t, foundCity)

		assert.Equal(t, testCities[1].Name, foundCity.Name)
		assert.Equal(t, testCities[1].Country, foundCity.Country)
	})

	t.Run("Find non-existent city", func(t *testing.T) {
		foundCity, err := testCityRepo.FindCityByNameAndCountry(ctx, "NonExistentCity", "NonExistentCountry")
		require.NoError(t, err)
		assert.Nil(t, foundCity)
	})

	t.Run("Find city with wrong country", func(t *testing.T) {
		foundCity, err := testCityRepo.FindCityByNameAndCountry(ctx, "TestCityFind1", "Spain")
		require.NoError(t, err)
		assert.Nil(t, foundCity, "Should not find city when country doesn't match")
	})
}

func TestCityRepository_EdgeCases_Integration(t *testing.T) {
	ctx := context.Background()
	clearCityTables(t)

	t.Run("Save city with special characters", func(t *testing.T) {
		city := locitypes.CityDetail{
			Name:            "TestCity-São_Paulo",
			Country:         "Brasil",
			StateProvince:   "São Paulo",
			AiSummary:       "City with special characters: áéíóú àèìòù ãõ ç",
			CenterLatitude:  f64(-23.5505),
			CenterLongitude: f64(-46.6333),
		}

		cityID, err := testCityRepo.SaveCity(ctx, city)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, cityID)

		// Verify can be found back
		foundCity, err := testCityRepo.FindCityByNameAndCountry(ctx, city.Name, city.Country)
		require.NoError(t, err)
		require.NotNil(t, foundCity)

		assert.Equal(t, city.Name, foundCity.Name)
		assert.Equal(t, city.AiSummary, foundCity.AiSummary)
	})

	t.Run("Save city with very long summary", func(t *testing.T) {
		longSummary := "This is a very long AI summary that could potentially exceed normal text lengths. " +
			"It contains detailed information about the city, its history, culture, attractions, " +
			"weather, transportation, demographics, economy, and much more detailed information that " +
			"an AI might generate when describing a city in great detail for travel planning purposes."

		city := locitypes.CityDetail{
			Name:            "TestCityLong",
			Country:         "TestCountry",
			AiSummary:       longSummary,
			CenterLatitude:  f64(0.0),
			CenterLongitude: f64(0.0),
		}

		cityID, err := testCityRepo.SaveCity(ctx, city)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, cityID)

		// Verify summary was saved correctly
		foundCity, err := testCityRepo.FindCityByNameAndCountry(ctx, city.Name, city.Country)
		require.NoError(t, err)
		require.NotNil(t, foundCity)

		assert.Equal(t, longSummary, foundCity.AiSummary)
	})

	t.Run("Coordinate precision test", func(t *testing.T) {
		city := locitypes.CityDetail{
			Name:            "TestCityPrecision",
			Country:         "TestCountry",
			AiSummary:       "City for testing coordinate precision",
			CenterLatitude:  f64(38.722252),
			CenterLongitude: f64(-9.139337),
		}

		cityID, err := testCityRepo.SaveCity(ctx, city)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, cityID)

		foundCity, err := testCityRepo.FindCityByNameAndCountry(ctx, city.Name, city.Country)
		require.NoError(t, err)
		require.NotNil(t, foundCity)

		// PostGIS should maintain reasonable precision for coordinates
		require.NotNil(t, foundCity.CenterLatitude)
		require.NotNil(t, foundCity.CenterLongitude)
		assert.InDelta(t, *city.CenterLatitude, *foundCity.CenterLatitude, 0.000001)
		assert.InDelta(t, *city.CenterLongitude, *foundCity.CenterLongitude, 0.000001)
	})
}
