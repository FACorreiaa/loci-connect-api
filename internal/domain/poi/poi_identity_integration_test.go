//go:build integration

package poi

import (
	"context"
	"io"
	"log/slog"
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// identityRepo builds a repository over the shared test pool. The existing
// harness exposes the service rather than the repo, and these tests exercise the
// identity layer directly.
func identityRepo(t *testing.T) Repository {
	t.Helper()
	return NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// The bug this fixes: POIs surfaced by search were generated with a fresh
// uuid.New() every call and never persisted, so the same place came back with a
// different id each time. GetPOI failed for all of them, and reviews, saves,
// favourites and list items — all of which have a foreign key to
// points_of_interest — could not attach to anything a user could actually see.
func TestUpsertPOIByIdentity_SamePlaceResolvesToOneRow(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "IdentityCity-"+uuid.NewString()[:8])

	place := locitypes.POIDetailedInfo{
		Name:           "Museu Nacional do Azulejo",
		Latitude:       38.7255,
		Longitude:      -9.1139,
		Category:       "museum",
		DescriptionPOI: "Tile museum",
	}

	first, inserted, err := identityRepo(t).UpsertPOIByIdentity(ctx, place, cityID)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, first)
	assert.True(t, inserted, "the first upsert of a place creates it")

	t.Run("an identical regeneration reuses the row", func(t *testing.T) {
		again, inserted, err := identityRepo(t).UpsertPOIByIdentity(ctx, place, cityID)
		require.NoError(t, err)
		assert.Equal(t, first, again, "the same place must keep one id across searches")
		assert.False(t, inserted, "a repeat must not report an insert; that is what stops us re-embedding it")
	})

	t.Run("kilometre-scale coordinate drift still matches", func(t *testing.T) {
		// This is the case that defeated a coordinate-based key: asked the same
		// question twice, the live LLM returned the same museums with coordinates
		// kilometres apart. Coordinates are not identity.
		drifted := place
		drifted.Latitude = 38.7139
		drifted.Longitude = -9.1394
		got, _, err := identityRepo(t).UpsertPOIByIdentity(ctx, drifted, cityID)
		require.NoError(t, err)
		assert.Equal(t, first, got, "the same name in the same city is the same place")
	})

	t.Run("case and whitespace differences still match", func(t *testing.T) {
		messy := place
		messy.Name = "  museu nacional do azulejo "
		got, _, err := identityRepo(t).UpsertPOIByIdentity(ctx, messy, cityID)
		require.NoError(t, err)
		assert.Equal(t, first, got)
	})

	t.Run("a differently named place gets its own row", func(t *testing.T) {
		other := locitypes.POIDetailedInfo{
			Name:      "Museu do Fado",
			Latitude:  38.7118,
			Longitude: -9.1256,
			Category:  "museum",
		}
		got, _, err := identityRepo(t).UpsertPOIByIdentity(ctx, other, cityID)
		require.NoError(t, err)
		assert.NotEqual(t, first, got)
	})

	// Documented trade-off: identity is (city, name), so two distinct venues
	// sharing a name in one city collapse into a single row. Acceptable for
	// LLM-sourced data; revisit when a real place provider supplies stable ids.
	t.Run("same name at a different location collapses, by design", func(t *testing.T) {
		sameName := place
		sameName.Latitude = 38.7355
		sameName.Longitude = -9.1239
		got, _, err := identityRepo(t).UpsertPOIByIdentity(ctx, sameName, cityID)
		require.NoError(t, err)
		assert.Equal(t, first, got)
	})

	t.Run("the persisted row is retrievable, which it never was before", func(t *testing.T) {
		got, err := identityRepo(t).GetPOIByID(ctx, first)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Museu Nacional do Azulejo", got.Name)
	})
}

// Later generations often carry less detail than earlier ones. Filling gaps is
// welcome; erasing what we already knew is not.
func TestUpsertPOIByIdentity_MergesRatherThanOverwrites(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "MergeCity-"+uuid.NewString()[:8])

	rich := locitypes.POIDetailedInfo{
		Name:           "Café Comercial",
		Latitude:       40.4250,
		Longitude:      -3.7010,
		Category:       "cafe",
		DescriptionPOI: "Historic café",
		Website:        "https://example.test/cafe",
		PhoneNumber:    "+34 900 000 000",
		Address:        "Glorieta de Bilbao 7",
	}
	id, _, err := identityRepo(t).UpsertPOIByIdentity(ctx, rich, cityID)
	require.NoError(t, err)

	// A sparser regeneration of the same place.
	sparse := locitypes.POIDetailedInfo{
		Name:      rich.Name,
		Latitude:  rich.Latitude,
		Longitude: rich.Longitude,
		Category:  "cafe",
	}
	again, _, err := identityRepo(t).UpsertPOIByIdentity(ctx, sparse, cityID)
	require.NoError(t, err)
	require.Equal(t, id, again)

	got, err := identityRepo(t).GetPOIByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "https://example.test/cafe", got.Website, "a sparser regeneration must not erase the website")
	assert.NotEmpty(t, got.DescriptionPOI, "nor the description")
}

// A POI with no city cannot be deduped — same-named places are indistinguishable
// without one — so it is refused rather than written as an un-dedupable row.
func TestUpsertPOIByIdentity_RequiresACity(t *testing.T) {
	_, _, err := identityRepo(t).UpsertPOIByIdentity(context.Background(), locitypes.POIDetailedInfo{
		Name: "Nowhere", Latitude: 1, Longitude: 1,
	}, uuid.Nil)
	require.Error(t, err)
}

// PersistGeneratedPOIs rewrites ids in place and reports how many landed.
func TestPersistGeneratedPOIs_AssignsStableIdsInPlace(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "BatchCity-"+uuid.NewString()[:8])

	batch := []locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "Alfa Bar", Latitude: 38.70, Longitude: -9.10, Category: "bar"},
		{ID: uuid.New(), Name: "Bravo Bakery", Latitude: 38.71, Longitude: -9.11, Category: "bakery"},
	}
	generated := []uuid.UUID{batch[0].ID, batch[1].ID}

	res := identityRepo(t).PersistGeneratedPOIs(ctx, batch, cityID)
	assert.Equal(t, 2, res.Persisted)
	assert.Len(t, res.NewIDs, 2, "both are new, so both need embeddings")

	for i := range batch {
		assert.NotEqual(t, generated[i], batch[i].ID, "the throwaway generated id must be replaced")
		assert.Equal(t, cityID, batch[i].CityID)

		got, err := identityRepo(t).GetPOIByID(ctx, batch[i].ID)
		require.NoError(t, err)
		require.NotNil(t, got)
	}

	// Re-running the same batch must not create new rows.
	rerun := []locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "Alfa Bar", Latitude: 38.70, Longitude: -9.10, Category: "bar"},
	}
	rerunRes := identityRepo(t).PersistGeneratedPOIs(ctx, rerun, cityID)
	require.Equal(t, 1, rerunRes.Persisted)
	assert.Empty(t, rerunRes.NewIDs, "nothing was created, so nothing needs re-embedding")
	assert.Equal(t, batch[0].ID, rerun[0].ID, "a repeated search must resolve to the same row")
}
