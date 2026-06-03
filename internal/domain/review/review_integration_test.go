//go:build integration

package review

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testReviewDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testReviewDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func clearReviewTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{"review_replies", "review_helpfuls", "reviews"} {
		_, err := testReviewDB.Exec(context.Background(), "DELETE FROM "+table)
		require.NoError(t, err, "Failed to clear %s", table)
	}
}

func createTestUserForReview(t *testing.T) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	email := "review-" + userID.String() + "@example.com"
	_, err := testReviewDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", userID, email)
	require.NoError(t, err)
	return userID
}

func createTestPOIForReview(t *testing.T) uuid.UUID {
	t.Helper()
	poiID := uuid.New()
	cityID := uuid.New()
	_, err := testReviewDB.Exec(context.Background(),
		"INSERT INTO cities (id, name, country) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
		cityID, "Review City", "Test Country")
	require.NoError(t, err)
	_, err = testReviewDB.Exec(context.Background(),
		"INSERT INTO points_of_interest (id, city_id, name, location) VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($4, $5), 4326))",
		poiID, cityID, "Test POI", -9.1393, 38.7223)
	require.NoError(t, err)
	return poiID
}

// insertReview inserts a review row and returns its id.
func insertReview(t *testing.T, userID, poiID uuid.UUID, rating int, title, content string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now()
	_, err := testReviewDB.Exec(context.Background(), `
		INSERT INTO reviews (id, user_id, poi_id, rating, title, content, helpful, unhelpful, is_verified, is_published, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 0, 0, false, true, $7, $7)`,
		id, userID, poiID, rating, title, content, now)
	require.NoError(t, err)
	return id
}

func TestReviewModels_Integration(t *testing.T) {
	ctx := context.Background()
	clearReviewTables(t)

	userID := createTestUserForReview(t)
	poiID := createTestPOIForReview(t)

	t.Run("Create and save review", func(t *testing.T) {
		id := uuid.New()
		now := time.Now()
		visitDate := now.AddDate(0, 0, -7)
		imageURLs := []string{"https://example.com/image1.jpg", "https://example.com/image2.jpg"}
		_, err := testReviewDB.Exec(ctx, `
			INSERT INTO reviews (id, user_id, poi_id, rating, title, content, visit_date, image_urls, helpful, unhelpful, is_verified, is_published, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, 0, false, true, $9, $9)`,
			id, userID, poiID, 5, "Amazing place!", "This is a fantastic location to visit.", visitDate, imageURLs, now)
		require.NoError(t, err)

		var dbTitle, dbContent string
		var dbRating int
		err = testReviewDB.QueryRow(ctx, "SELECT title, content, rating FROM reviews WHERE id = $1", id).
			Scan(&dbTitle, &dbContent, &dbRating)
		require.NoError(t, err)
		assert.Equal(t, "Amazing place!", dbTitle)
		assert.Equal(t, "This is a fantastic location to visit.", dbContent)
		assert.Equal(t, 5, dbRating)
	})

	t.Run("Create and save review helpful", func(t *testing.T) {
		reviewID := insertReview(t, userID, poiID, 4, "Good place", "Nice location")
		otherUserID := createTestUserForReview(t)

		_, err := testReviewDB.Exec(ctx, `
			INSERT INTO review_helpfuls (user_id, review_id, is_helpful, created_at)
			VALUES ($1, $2, $3, $4)`,
			otherUserID, reviewID, true, time.Now())
		require.NoError(t, err)

		var dbIsHelpful bool
		err = testReviewDB.QueryRow(ctx,
			"SELECT is_helpful FROM review_helpfuls WHERE user_id = $1 AND review_id = $2",
			otherUserID, reviewID).Scan(&dbIsHelpful)
		require.NoError(t, err)
		assert.True(t, dbIsHelpful)
	})

	t.Run("Create and save review reply", func(t *testing.T) {
		reviewID := insertReview(t, userID, poiID, 3, "Average place", "It was okay")
		replyUserID := createTestUserForReview(t)
		replyID := uuid.New()
		now := time.Now()

		_, err := testReviewDB.Exec(ctx, `
			INSERT INTO review_replies (id, review_id, user_id, content, is_official, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			replyID, reviewID, replyUserID, "Thanks for your feedback!", false, now)
		require.NoError(t, err)

		var dbContent string
		var dbIsOfficial bool
		err = testReviewDB.QueryRow(ctx, "SELECT content, is_official FROM review_replies WHERE id = $1", replyID).
			Scan(&dbContent, &dbIsOfficial)
		require.NoError(t, err)
		assert.Equal(t, "Thanks for your feedback!", dbContent)
		assert.False(t, dbIsOfficial)
	})
}

func TestReviewQueries_Integration(t *testing.T) {
	ctx := context.Background()
	clearReviewTables(t)

	userID := createTestUserForReview(t)
	poiID := createTestPOIForReview(t)

	insertReview(t, userID, poiID, 5, "Excellent!", "Perfect place")
	insertReview(t, userID, poiID, 4, "Very good", "Really enjoyed it")
	insertReview(t, userID, poiID, 3, "Average", "It was okay")

	t.Run("Get reviews by POI ordered by rating", func(t *testing.T) {
		rows, err := testReviewDB.Query(ctx, "SELECT rating FROM reviews WHERE poi_id = $1 ORDER BY rating DESC", poiID)
		require.NoError(t, err)
		defer rows.Close()

		var ratings []int
		for rows.Next() {
			var r int
			require.NoError(t, rows.Scan(&r))
			ratings = append(ratings, r)
		}
		require.NoError(t, rows.Err())
		assert.Equal(t, []int{5, 4, 3}, ratings)
	})

	t.Run("Calculate average rating", func(t *testing.T) {
		var avgRating float64
		err := testReviewDB.QueryRow(ctx, "SELECT AVG(rating::numeric) FROM reviews WHERE poi_id = $1", poiID).Scan(&avgRating)
		require.NoError(t, err)
		assert.InDelta(t, float64(5+4+3)/3, avgRating, 0.01)
	})

	t.Run("Count reviews by user", func(t *testing.T) {
		var count int
		err := testReviewDB.QueryRow(ctx, "SELECT COUNT(*) FROM reviews WHERE user_id = $1", userID).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 3, count)
	})
}

func TestReviewConstraints_Integration(t *testing.T) {
	ctx := context.Background()
	clearReviewTables(t)

	userID := createTestUserForReview(t)
	poiID := createTestPOIForReview(t)

	t.Run("Rating CHECK constraint rejects out-of-range rating", func(t *testing.T) {
		// Valid rating succeeds.
		_ = insertReview(t, userID, poiID, 5, "Valid rating", "Content")

		// rating must be between 1 and 5 (CHECK constraint).
		_, err := testReviewDB.Exec(ctx, `
			INSERT INTO reviews (id, user_id, poi_id, rating, title, content, helpful, unhelpful, is_verified, is_published, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 0, 0, false, true, $7, $7)`,
			uuid.New(), userID, poiID, 10, "Invalid rating", "Content", time.Now())
		require.Error(t, err, "rating=10 should violate the CHECK constraint")
	})
}
