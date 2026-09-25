//go:build integration

package review

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
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

func TestReviewRepo_OnePerUserPOI_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	author, poiID := seedUser(t, pool), seedPOI(t, pool)

	require.NoError(t, repo.Create(ctx, &Review{UserID: author, POIID: poiID, Rating: 4, Content: "first"}))
	err := repo.Create(ctx, &Review{UserID: author, POIID: poiID, Rating: 2, Content: "again"})
	require.ErrorIs(t, err, ErrAlreadyExists)

	// Another user may still review the same place.
	require.NoError(t, repo.Create(ctx, &Review{UserID: seedUser(t, pool), POIID: poiID, Rating: 5, Content: "mine"}))
}

func TestReviewRepo_CreateUnknownPOI_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	err := repo.Create(context.Background(), &Review{UserID: seedUser(t, pool), POIID: uuid.New(), Rating: 4, Content: "x"})
	require.ErrorIs(t, err, ErrPOINotFound)
}

func TestReviewRepo_NullTitleAndMemberSince_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	author, poiID := seedUser(t, pool), seedPOI(t, pool)
	id := uuid.New()
	// Rows written before titles were sent as '' carry NULL, which used to
	// fail the scan into a string.
	_, err := pool.Exec(ctx,
		`INSERT INTO reviews (id, user_id, poi_id, rating, title, content) VALUES ($1, $2, $3, 4, NULL, 'no title')`,
		id, author, poiID)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "", got.Title)
	assert.False(t, got.ReviewerSince.IsZero(), "member_since comes from users.created_at")

	list, _, err := repo.ListByPOI(ctx, poiID, 10, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestReviewRepo_Update_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	author, poiID := seedUser(t, pool), seedPOI(t, pool)
	r := &Review{UserID: author, POIID: poiID, Rating: 5, Title: "Great", Content: "Loved it", Photos: []string{"a.jpg"}}
	require.NoError(t, repo.Create(ctx, r))

	// Someone else cannot edit it, and it is indistinguishable from missing.
	err := repo.Update(ctx, r.ID, seedUser(t, pool), UpdateFields{Rating: 1, Content: "hijack"})
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, repo.Update(ctx, uuid.New(), author, UpdateFields{Rating: 1, Content: "x"}), ErrNotFound)

	require.NoError(t, repo.Update(ctx, r.ID, author, UpdateFields{Rating: 2, Title: "", Content: "Meh on a second visit"}))
	got, err := repo.GetByID(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Rating)
	assert.Equal(t, "", got.Title)
	assert.Equal(t, "Meh on a second visit", got.Content)
	assert.Empty(t, got.Photos, "an update replaces the photo list")

	var avg float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT average_rating FROM points_of_interest WHERE id = $1`, poiID).Scan(&avg))
	assert.InDelta(t, 2.0, avg, 0.001, "the POI rating trigger follows the edit")
}

func TestReviewRepo_Statistics_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	poiID := seedPOI(t, pool)

	empty, err := repo.Statistics(ctx, poiID)
	require.NoError(t, err)
	assert.Equal(t, 0, empty.TotalReviews)
	assert.InDelta(t, 0.0, empty.AverageRating, 0)
	assert.Nil(t, empty.LastReviewAt)

	for _, rating := range []int{5, 5, 4, 1} {
		require.NoError(t, repo.Create(ctx, &Review{UserID: seedUser(t, pool), POIID: poiID, Rating: rating, Content: "x"}))
	}
	// Unpublished reviews do not count.
	hidden := &Review{UserID: seedUser(t, pool), POIID: poiID, Rating: 3, Content: "hidden"}
	require.NoError(t, repo.Create(ctx, hidden))
	_, err = pool.Exec(ctx, `UPDATE reviews SET is_published = false WHERE id = $1`, hidden.ID)
	require.NoError(t, err)
	// Nor do other POIs' reviews.
	require.NoError(t, repo.Create(ctx, &Review{UserID: seedUser(t, pool), POIID: seedPOI(t, pool), Rating: 1, Content: "x"}))

	st, err := repo.Statistics(ctx, poiID)
	require.NoError(t, err)
	assert.Equal(t, poiID, st.POIID)
	assert.Equal(t, 4, st.TotalReviews)
	assert.InDelta(t, 3.75, st.AverageRating, 0.001)
	assert.Equal(t, [5]int{1, 0, 0, 1, 2}, st.Distribution)
	assert.NotNil(t, st.LastReviewAt)
}

func TestReviewRepo_UnlikeDeletesVote_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	r := &Review{UserID: seedUser(t, pool), POIID: seedPOI(t, pool), Rating: 4, Content: "x"}
	require.NoError(t, repo.Create(ctx, r))
	v1, v2 := seedUser(t, pool), seedUser(t, pool)

	count, err := repo.SetHelpful(ctx, v1, r.ID, true)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	count, err = repo.SetHelpful(ctx, v2, r.ID, true)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	// Liking twice is idempotent.
	count, err = repo.SetHelpful(ctx, v2, r.ID, true)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	count, err = repo.SetHelpful(ctx, v1, r.ID, false)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var rows, unhelpful int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_helpfuls WHERE review_id = $1 AND user_id = $2`, r.ID, v1).Scan(&rows))
	assert.Equal(t, 0, rows, "un-like removes the vote instead of recording an unhelpful one")
	require.NoError(t, pool.QueryRow(ctx, `SELECT unhelpful FROM reviews WHERE id = $1`, r.ID).Scan(&unhelpful))
	assert.Equal(t, 0, unhelpful)

	// Un-liking something never liked is a no-op.
	count, err = repo.SetHelpful(ctx, seedUser(t, pool), r.ID, false)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	_, err = repo.SetHelpful(ctx, v1, uuid.New(), true)
	require.ErrorIs(t, err, ErrNotFound, "a missing review is NotFound, not an FK Internal")
}

func TestReviewMigration_DedupesKeepingNewest_Integration(t *testing.T) {
	_, pool := newRepo(t)
	ctx := context.Background()
	author, poiID := seedUser(t, pool), seedPOI(t, pool)

	// Re-run 0103's dedupe against duplicates seeded with the constraint off.
	_, err := pool.Exec(ctx, `ALTER TABLE reviews DROP CONSTRAINT reviews_user_poi_unique`)
	require.NoError(t, err)
	t.Cleanup(func() {
		testsupport.Truncate(t, pool, "review_replies", "review_helpfuls", "reviews")
		_, _ = pool.Exec(context.Background(),
			`ALTER TABLE reviews ADD CONSTRAINT reviews_user_poi_unique UNIQUE (user_id, poi_id)`)
	})
	older, newer := uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO reviews (id, user_id, poi_id, rating, content, created_at) VALUES
		($1, $3, $4, 2, 'old', NOW() - INTERVAL '2 days'),
		($2, $3, $4, 5, 'new', NOW())`, older, newer, author, poiID)
	require.NoError(t, err)

	up, err := os.ReadFile("../../../pkg/db/migrations/0103_reviews_one_per_user_poi.up.sql")
	require.NoError(t, err)
	body := strings.SplitN(string(up), "-- +goose Down", 2)[0]
	body = strings.NewReplacer("-- +goose Up", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(body)
	_, err = pool.Exec(ctx, body)
	require.NoError(t, err)

	var ids []uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM reviews WHERE user_id = $1 AND poi_id = $2`, author, poiID)
	require.NoError(t, err)
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []uuid.UUID{newer}, ids)
}

// The summary My reviews shows, from the rows: the server used to leave
// UserReviewStatistics empty and both clients recomputed it.
func TestReviewRepo_UserStatistics_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	author, voter := seedUser(t, pool), seedUser(t, pool)
	p1, p2 := seedPOI(t, pool), seedPOI(t, pool)

	empty, err := repo.UserStatistics(ctx, author)
	require.NoError(t, err)
	assert.Equal(t, &UserStatistics{}, empty)

	first := &Review{UserID: author, POIID: p1, Rating: 4, Content: "good enough"}
	require.NoError(t, repo.Create(ctx, first))
	require.NoError(t, repo.Create(ctx, &Review{UserID: author, POIID: p2, Rating: 5, Content: "loved it"}))
	_, err = repo.SetHelpful(ctx, voter, first.ID, true)
	require.NoError(t, err)

	got, err := repo.UserStatistics(ctx, author)
	require.NoError(t, err)
	assert.Equal(t, 2, got.TotalReviews)
	assert.InDelta(t, 4.5, got.AverageRatingGiven, 0.001)
	assert.Equal(t, 1, got.HelpfulVotesReceived)
}
