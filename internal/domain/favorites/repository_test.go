//go:build integration

package favorites

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFavoritesRepo(t *testing.T) (Repository, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testsupport.StartPostgres(t)
	testsupport.Truncate(t, pool, "user_favorites", "users")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRepository(pool, logger), pool
}

func insertUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	email := fmt.Sprintf("fav-%s@example.com", uuid.NewString())
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email) VALUES ($1) RETURNING id", email).Scan(&id)
	require.NoError(t, err, "insert test user")
	return id
}

func newFavorite(userID uuid.UUID, itemID uuid.UUID, contentType, name string) *locitypes.FavoriteItem {
	return &locitypes.FavoriteItem{
		UserID:      userID,
		ItemID:      itemID.String(),
		ItemName:    name,
		ContentType: contentType,
		CityName:    "Lisbon",
		Latitude:    38.72,
		Longitude:   -9.14,
		Rating:      4.5,
		Category:    "landmark",
	}
}

func TestAddAndIsFavorited(t *testing.T) {
	repo, pool := newFavoritesRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	itemID := uuid.New()

	got, err := repo.AddFavorite(ctx, newFavorite(userID, itemID, "poi", "Belem Tower"))
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, got.ID)
	assert.False(t, got.AddedAt.IsZero(), "added_at should be set")

	favorited, err := repo.IsFavorited(ctx, userID, itemID, "poi")
	require.NoError(t, err)
	assert.True(t, favorited)

	// Different content type for same item is not favorited.
	favorited, err = repo.IsFavorited(ctx, userID, itemID, "hotel")
	require.NoError(t, err)
	assert.False(t, favorited)
}

func TestAddFavorite_UpsertOnConflict(t *testing.T) {
	repo, pool := newFavoritesRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	itemID := uuid.New()

	first := newFavorite(userID, itemID, "poi", "Original")
	first.Notes = "first note"
	_, err := repo.AddFavorite(ctx, first)
	require.NoError(t, err)

	second := newFavorite(userID, itemID, "poi", "Original")
	second.Notes = "updated note"
	_, err = repo.AddFavorite(ctx, second)
	require.NoError(t, err)

	// Conflict on (user_id, item_id, content_type) must not duplicate.
	count, err := repo.GetFavoritesCount(ctx, userID, "poi")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGetFavorites_FilterAndCount(t *testing.T) {
	repo, pool := newFavoritesRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)

	_, err := repo.AddFavorite(ctx, newFavorite(userID, uuid.New(), "poi", "POI A"))
	require.NoError(t, err)
	_, err = repo.AddFavorite(ctx, newFavorite(userID, uuid.New(), "poi", "POI B"))
	require.NoError(t, err)
	_, err = repo.AddFavorite(ctx, newFavorite(userID, uuid.New(), "hotel", "Hotel A"))
	require.NoError(t, err)

	pois, total, err := repo.GetFavorites(ctx, userID, "poi", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, pois, 2)

	all, totalAll, err := repo.GetFavorites(ctx, userID, "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, totalAll)
	assert.Len(t, all, 3)

	hotelCount, err := repo.GetFavoritesCount(ctx, userID, "hotel")
	require.NoError(t, err)
	assert.Equal(t, 1, hotelCount)
}

func TestRemoveFavorite(t *testing.T) {
	repo, pool := newFavoritesRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	itemID := uuid.New()

	_, err := repo.AddFavorite(ctx, newFavorite(userID, itemID, "poi", "ToRemove"))
	require.NoError(t, err)

	require.NoError(t, repo.RemoveFavorite(ctx, userID, itemID, "poi"))

	favorited, err := repo.IsFavorited(ctx, userID, itemID, "poi")
	require.NoError(t, err)
	assert.False(t, favorited)

	// Removing a non-existent favorite is a no-op, not an error.
	require.NoError(t, repo.RemoveFavorite(ctx, userID, uuid.New(), "poi"))
}
