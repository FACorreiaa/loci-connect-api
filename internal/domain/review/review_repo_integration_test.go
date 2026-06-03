//go:build integration

package review

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRepo(t *testing.T) (Repository, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testsupport.StartPostgres(t)
	testsupport.Truncate(t, pool, "review_replies", "review_helpfuls", "reviews")
	return NewRepository(pool, slog.New(slog.NewTextHandler(io.Discard, nil))), pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, "rev-"+id.String()+"@example.com")
	require.NoError(t, err)
	return id
}

func seedPOI(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	poiID, cityID := uuid.New(), uuid.New()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO cities (id, name, country) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING", cityID, "RevCity", "X")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		"INSERT INTO points_of_interest (id, city_id, name, location) VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($4,$5),4326))",
		poiID, cityID, "Rev POI", -9.1, 38.7)
	require.NoError(t, err)
	return poiID
}

func TestReviewRepo_CRUD_Helpful_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	author := seedUser(t, pool)
	poiID := seedPOI(t, pool)

	r := &Review{UserID: author, POIID: poiID, Rating: 5, Title: "Great", Content: "Loved it", Photos: []string{"a.jpg"}}
	require.NoError(t, repo.Create(ctx, r))
	require.NotEqual(t, uuid.Nil, r.ID)
	assert.False(t, r.CreatedAt.IsZero())

	got, err := repo.GetByID(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, 5, got.Rating)
	assert.Equal(t, "Loved it", got.Content)
	assert.Equal(t, []string{"a.jpg"}, got.Photos)

	list, total, err := repo.ListByPOI(ctx, poiID, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, list, 1)

	byUser, total, err := repo.ListByUser(ctx, author, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, byUser, 1)

	// A different user marks it helpful.
	voter := seedUser(t, pool)
	count, err := repo.SetHelpful(ctx, voter, r.ID, true)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	// Toggling the same voter to unhelpful drops the helpful count to 0.
	count, err = repo.SetHelpful(ctx, voter, r.ID, false)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	require.NoError(t, repo.Delete(ctx, r.ID, author))
	_, err = repo.GetByID(ctx, r.ID)
	require.ErrorIs(t, err, ErrNotFound)

	// Delete by a non-owner / missing row returns ErrNotFound.
	require.ErrorIs(t, repo.Delete(ctx, uuid.New(), author), ErrNotFound)
}
