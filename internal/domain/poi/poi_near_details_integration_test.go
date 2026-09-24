//go:build integration

package poi

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rows written by the chat flow leave most columns NULL. GetHotelByID and
// GetRestaurantByID used to scan those straight into strings and fail, so a
// saved hotel could not be opened even when its row existed.
func TestHotelAndRestaurantLookups_ToleratePartialRows(t *testing.T) {
	ctx := context.Background()
	repo := identityRepo(t)
	cityID := uuid.New()
	cityName := "NearCity-" + uuid.NewString()[:8]
	insertTestCity(t, cityID, cityName)

	hotelID, restID := uuid.New(), uuid.New()
	_, err := testDB.Exec(ctx, `
		INSERT INTO hotel_details (id, city_id, name, latitude, longitude, location)
		VALUES ($1, $2, 'Casa do Largo', 38.7100, -9.1400, ST_SetSRID(ST_MakePoint(-9.1400, 38.7100), 4326))`,
		hotelID, cityID)
	require.NoError(t, err)
	_, err = testDB.Exec(ctx, `
		INSERT INTO restaurant_details (id, city_id, name, latitude, longitude, location, cuisine_type, opening_hours)
		VALUES ($1, $2, 'Taberna', 38.7105, -9.1405, ST_SetSRID(ST_MakePoint(-9.1405, 38.7105), 4326),
		        'Portuguese', '{"monday":"12:00-23:00"}')`,
		restID, cityID)
	require.NoError(t, err)

	t.Run("by id", func(t *testing.T) {
		hotel, err := repo.GetHotelByID(ctx, hotelID)
		require.NoError(t, err)
		require.NotNil(t, hotel)
		assert.Equal(t, "Casa do Largo", hotel.Name)

		rest, err := repo.GetRestaurantByID(ctx, restID)
		require.NoError(t, err)
		require.NotNil(t, rest)
		require.NotNil(t, rest.CuisineType)
		assert.Equal(t, "Portuguese", *rest.CuisineType)
	})

	t.Run("near, nearest first, with city", func(t *testing.T) {
		hotels, err := repo.FindHotelsNear(ctx, 38.7100, -9.1400, 500, 10)
		require.NoError(t, err)
		require.NotEmpty(t, hotels)
		assert.Equal(t, hotelID, hotels[0].ID)
		assert.Equal(t, cityName, hotels[0].City)

		rests, err := repo.FindRestaurantsNear(ctx, 38.7100, -9.1400, 500, 10)
		require.NoError(t, err)
		require.NotEmpty(t, rests)
		assert.Equal(t, restID, rests[0].ID)
		require.NotNil(t, rests[0].OpeningHours)
		assert.JSONEq(t, `{"monday":"12:00-23:00"}`, *rests[0].OpeningHours)
	})

	t.Run("outside the radius", func(t *testing.T) {
		hotels, err := repo.FindHotelsNear(ctx, 41.15, -8.61, 500, 10) // Porto
		require.NoError(t, err)
		for _, h := range hotels {
			assert.NotEqual(t, hotelID, h.ID)
		}
	})
}

// GetItinerary backs the saved-itinerary page; a missing or foreign id must be
// distinguishable from a database failure so the handler can answer NotFound.
func TestGetItinerary_NotFoundWrapsErrNoRows(t *testing.T) {
	_, err := identityRepo(t).GetItinerary(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, pgx.ErrNoRows)
}
