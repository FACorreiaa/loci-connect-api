//go:build integration

package poi

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// SearchPOI with no query (web's "near me" list) is answered by SearchPOIs:
// places within the radius, nearest first, optionally one category. This runs
// the real PostGIS query, which nothing exercised before.
func TestSearchPOIs_NearbyListingNearestFirst(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Nearby City "+cityID.String()[:8])

	// Somewhere empty so other tests' rows cannot fall inside the radius.
	const lat, lon = -45.0, 170.0
	seed := func(name, category string, dLat float64) uuid.UUID {
		id := uuid.New()
		_, err := testDB.Exec(ctx, `
			INSERT INTO points_of_interest (id, city_id, name, category, location)
			VALUES ($1, $2, $3, $4, ST_SetSRID(ST_MakePoint($5, $6), 4326))`,
			id, cityID, name, category, lon, lat+dLat)
		require.NoError(t, err)
		return id
	}
	far := seed("far cafe", "cafe", 0.02)        // ~2.2 km
	near := seed("near museum", "museum", 0.001) // ~110 m
	mid := seed("mid cafe", "cafe", 0.01)        // ~1.1 km
	seed("out of range", "cafe", 0.5)            // ~55 km

	filter := locitypes.POIFilter{Location: locitypes.GeoPoint{Latitude: lat, Longitude: lon}, Radius: 5}
	got, err := testService.SearchPOIs(ctx, filter)
	require.NoError(t, err)
	ids := make([]uuid.UUID, 0, len(got))
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	require.Equal(t, []uuid.UUID{near, mid, far}, ids, "within radius, nearest first")
	require.Greater(t, got[2].Distance, got[0].Distance)

	filter.Category = "cafe"
	got, err = testService.SearchPOIs(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, mid, got[0].ID)
}

// The nearby listing's cap is a LIMIT in the statement, after ORDER BY
// distance: asking for two returns the two nearest, not an arbitrary two and
// not every place in the radius.
func TestSearchPOIs_LimitKeepsTheNearest(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Limit City "+cityID.String()[:8])

	// Another empty spot, apart from the test above.
	const lat, lon = -46.0, 171.0
	seed := func(name string, dLat float64) uuid.UUID {
		id := uuid.New()
		_, err := testDB.Exec(ctx, `
			INSERT INTO points_of_interest (id, city_id, name, category, location)
			VALUES ($1, $2, $3, 'cafe', ST_SetSRID(ST_MakePoint($4, $5), 4326))`,
			id, cityID, name, lon, lat+dLat)
		require.NoError(t, err)
		return id
	}
	// Inserted far-to-near so insertion order cannot pass for distance order.
	seed("fourth", 0.04)
	seed("third", 0.03)
	second := seed("second", 0.02)
	first := seed("first", 0.001)

	filter := locitypes.POIFilter{Location: locitypes.GeoPoint{Latitude: lat, Longitude: lon}, Radius: 10, Limit: 2}
	got, err := testService.SearchPOIs(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, first, got[0].ID)
	require.Equal(t, second, got[1].ID)

	// With a category the cap still binds to the right placeholder.
	filter.Category = "cafe"
	filter.Limit = 3
	got, err = testService.SearchPOIs(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, first, got[0].ID)

	// Zero means uncapped.
	filter.Limit = 0
	got, err = testService.SearchPOIs(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got, 4)
}
