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
		"INSERT INTO users (id, email, username) VALUES ($1, $2, $3)",
		id, "rev-"+id.String()+"@example.com", "revuser-"+id.String()[:8])
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
	// Enrichment from the users + points_of_interest joins.
	assert.Equal(t, "Rev POI", got.POIName)
	assert.NotEmpty(t, got.ReviewerName)

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

func TestReviewRepo_ListRecent_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()

	u1, u2 := seedUser(t, pool), seedUser(t, pool)
	p1, p2 := seedPOI(t, pool), seedPOI(t, pool)
	require.NoError(t, repo.Create(ctx, &Review{UserID: u1, POIID: p1, Rating: 4, Content: "first"}))
	require.NoError(t, repo.Create(ctx, &Review{UserID: u2, POIID: p2, Rating: 5, Content: "second"}))

	list, total, err := repo.ListRecent(ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, list, 2)
	// Newest first, and enriched with POI name.
	assert.Equal(t, "second", list[0].Content)
	assert.Equal(t, "Rev POI", list[0].POIName)
}
