//go:build integration

package review

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewRepo_VotedBy_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	poiA, poiB := seedPOI(t, pool), seedPOI(t, pool)
	author := seedUser(t, pool)
	a := &Review{UserID: author, POIID: poiA, Rating: 4, Content: "a"}
	b := &Review{UserID: author, POIID: poiB, Rating: 3, Content: "b"}
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.Create(ctx, b))
	voter, other := seedUser(t, pool), seedUser(t, pool)
	_, err := repo.SetHelpful(ctx, voter, a.ID, true)
	require.NoError(t, err)
	_, err = repo.SetHelpful(ctx, other, b.ID, true)
	require.NoError(t, err)

	got, err := repo.VotedBy(ctx, voter, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]bool{a.ID: true}, got, "only the viewer's own votes count")

	// Un-voting clears it.
	_, err = repo.SetHelpful(ctx, voter, a.ID, false)
	require.NoError(t, err)
	got, err = repo.VotedBy(ctx, voter, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = repo.VotedBy(ctx, uuid.Nil, []uuid.UUID{a.ID})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReviewRepo_GetByUserAndPOI_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	poiID := seedPOI(t, pool)
	me, someoneElse := seedUser(t, pool), seedUser(t, pool)

	_, err := repo.GetByUserAndPOI(ctx, me, poiID)
	require.ErrorIs(t, err, ErrNotFound)

	theirs := &Review{UserID: someoneElse, POIID: poiID, Rating: 2, Content: "meh"}
	require.NoError(t, repo.Create(ctx, theirs))
	_, err = repo.GetByUserAndPOI(ctx, me, poiID)
	require.ErrorIs(t, err, ErrNotFound, "another user's review of the place is not mine")

	mine := &Review{UserID: me, POIID: poiID, Rating: 5, Content: "great"}
	require.NoError(t, repo.Create(ctx, mine))
	got, err := repo.GetByUserAndPOI(ctx, me, poiID)
	require.NoError(t, err)
	assert.Equal(t, mine.ID, got.ID)
	assert.Equal(t, "Rev POI", got.POIName)
}

func TestReviewRepo_UserStatisticsDistribution_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	me := seedUser(t, pool)

	st, err := repo.UserStatistics(ctx, me)
	require.NoError(t, err)
	assert.Equal(t, UserStatistics{}, *st, "no reviews yields zeros")
	// TestReviewRepo_UserStatistics_Integration covers count, average and helpful votes; this pins the star distribution.

	for _, rating := range []int{5, 4, 4} {
		r := &Review{UserID: me, POIID: seedPOI(t, pool), Rating: rating, Content: "x"}
		require.NoError(t, repo.Create(ctx, r))
		if rating == 5 {
			_, err = repo.SetHelpful(ctx, seedUser(t, pool), r.ID, true)
			require.NoError(t, err)
			_, err = repo.SetHelpful(ctx, seedUser(t, pool), r.ID, true)
			require.NoError(t, err)
		}
	}
	// Someone else's review does not count towards mine.
	require.NoError(t, repo.Create(ctx, &Review{UserID: seedUser(t, pool), POIID: seedPOI(t, pool), Rating: 1, Content: "x"}))

	st, err = repo.UserStatistics(ctx, me)
	require.NoError(t, err)
	assert.Equal(t, 3, st.TotalReviews)
	assert.InDelta(t, 13.0/3.0, st.AverageRatingGiven, 1e-9)
	assert.Equal(t, [5]int{0, 0, 0, 2, 1}, st.Distribution)
	assert.Equal(t, 2, st.HelpfulVotesReceived)
}

func TestReviewRepo_Report_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	author, reporter := seedUser(t, pool), seedUser(t, pool)
	r := &Review{UserID: author, POIID: seedPOI(t, pool), Rating: 1, Content: "buy followers at ..."}
	require.NoError(t, repo.Create(ctx, r))

	require.NoError(t, repo.Report(ctx, reporter, r.ID, "spam", "a link farm"))
	// Reporting again replaces the reason; still one row.
	require.NoError(t, repo.Report(ctx, reporter, r.ID, "fake", ""))
	var rows int
	var reason string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*), MAX(reason) FROM review_reports WHERE review_id = $1`, r.ID).Scan(&rows, &reason))
	assert.Equal(t, 1, rows)
	assert.Equal(t, "fake", reason)

	require.NoError(t, repo.Report(ctx, seedUser(t, pool), r.ID, "offensive", ""))
	got, err := repo.GetByID(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.ReportCount, "report_count is distinct reporters")

	require.ErrorIs(t, repo.Report(ctx, author, r.ID, "spam", ""), ErrOwnReview)
	require.ErrorIs(t, repo.Report(ctx, author, r.ID, "spam", ""), ErrReportOwnReview)
	require.ErrorIs(t, repo.Report(ctx, reporter, uuid.New(), "spam", ""), ErrNotFound)
	require.Error(t, repo.Report(ctx, reporter, r.ID, "rude", ""), "the CHECK constraint backs the reason list")

	// Deleting the review takes its reports with it.
	require.NoError(t, repo.Delete(ctx, r.ID, author))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_reports WHERE review_id = $1`, r.ID).Scan(&rows))
	assert.Zero(t, rows)
}
