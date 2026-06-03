//go:build integration

package poi

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	cityrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testDB      *pgxpool.Pool
	testService Service
)

// stubDiscover satisfies the discoverRepo dependency of NewServiceImpl.
type stubDiscover struct{}

func (stubDiscover) TrackSearch(_ context.Context, _ uuid.UUID, _, _, _ string, _ int) error {
	return nil
}

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	// The POI service builds Gemini clients from env at construction (no network
	// call happens until a request); provide dummy values so it constructs.
	if os.Getenv("GEMINI_API_KEY") == "" {
		_ = os.Setenv("GEMINI_API_KEY", "test-key")
	}
	if os.Getenv("GEMINI_MODEL") == "" {
		_ = os.Setenv("GEMINI_MODEL", "gemini-1.5-flash")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	realRepo := NewRepository(testDB, logger)
	cityRepo := cityrepo.NewCityRepository(testDB, logger)
	testService = NewServiceImpl(realRepo, nil, cityRepo, stubDiscover{}, logger)
	os.Exit(m.Run())
}

func clearFavouritesTable(t *testing.T) {
	t.Helper()
	_, err := testDB.Exec(context.Background(), "DELETE FROM user_favorite_pois")
	require.NoError(t, err, "Failed to clear user_favorite_pois table")
}

func insertTestUser(t *testing.T, id uuid.UUID) {
	t.Helper()
	email := "poi-" + id.String() + "@example.com"
	_, err := testDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING", id, email)
	require.NoError(t, err)
}

func insertTestCity(t *testing.T, id uuid.UUID, name string) {
	t.Helper()
	_, err := testDB.Exec(context.Background(),
		"INSERT INTO cities (id, name, country) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
		id, name, "Testland")
	require.NoError(t, err)
}

// insertTestPOI seeds a minimal points_of_interest row (name + PostGIS location
// are the only NOT NULL columns without defaults).
func insertTestPOI(t *testing.T, id uuid.UUID, name string, cityID uuid.UUID, lat, lon float64) {
	t.Helper()
	_, err := testDB.Exec(context.Background(),
		"INSERT INTO points_of_interest (id, city_id, name, location) VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($4, $5), 4326)) ON CONFLICT (id) DO NOTHING",
		id, cityID, name, lon, lat)
	require.NoError(t, err)
}

func TestPOIServiceImpl_Favourites_Integration(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	poiID := uuid.New()
	cityID := uuid.New()

	insertTestUser(t, userID)
	insertTestCity(t, cityID, "Fav City")
	insertTestPOI(t, poiID, "Favourite Test POI 1", cityID, 1.0, 1.0)

	t.Run("Add and Get Favourite POI", func(t *testing.T) {
		clearFavouritesTable(t)

		favID, err := testService.AddPoiToFavourites(ctx, userID, poiID, false)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, favID)

		favourites, err := testService.GetFavouritePOIsByUserID(ctx, userID)
		require.NoError(t, err)
		require.Len(t, favourites, 1)
		assert.Equal(t, poiID, favourites[0].ID)
		assert.Equal(t, "Favourite Test POI 1", favourites[0].Name)
	})

	t.Run("Remove Favourite POI", func(t *testing.T) {
		clearFavouritesTable(t)
		_, err := testService.AddPoiToFavourites(ctx, userID, poiID, false)
		require.NoError(t, err)

		err = testService.RemovePoiFromFavourites(ctx, userID, poiID, false)
		require.NoError(t, err)

		favourites, err := testService.GetFavouritePOIsByUserID(ctx, userID)
		require.NoError(t, err)
		assert.Empty(t, favourites)
	})

	t.Run("Get Favourites for user with no favourites", func(t *testing.T) {
		clearFavouritesTable(t)
		otherUserID := uuid.New()
		insertTestUser(t, otherUserID)

		favourites, err := testService.GetFavouritePOIsByUserID(ctx, otherUserID)
		require.NoError(t, err)
		assert.Empty(t, favourites)
	})
}

func TestPOIServiceImpl_GetPOIsByCityID_Integration(t *testing.T) {
	ctx := context.Background()
	cityID1 := uuid.New()
	cityID2 := uuid.New()

	insertTestCity(t, cityID1, "CityWithPOIs")
	insertTestCity(t, cityID2, "CityWithoutPOIs")

	poiA := uuid.New()
	poiB := uuid.New()
	insertTestPOI(t, poiA, "City1 POI A", cityID1, 10.0, 10.0)
	insertTestPOI(t, poiB, "City1 POI B", cityID1, 10.1, 10.1)

	t.Run("Get POIs for city with POIs", func(t *testing.T) {
		pois, err := testService.GetPOIsByCityID(ctx, cityID1)
		require.NoError(t, err)
		require.Len(t, pois, 2)

		found := map[uuid.UUID]bool{}
		for _, p := range pois {
			found[p.ID] = true
		}
		assert.True(t, found[poiA], "POI A should be found")
		assert.True(t, found[poiB], "POI B should be found")
	})

	t.Run("Get POIs for city with no POIs", func(t *testing.T) {
		pois, err := testService.GetPOIsByCityID(ctx, cityID2)
		require.NoError(t, err)
		assert.Empty(t, pois)
	})
}
