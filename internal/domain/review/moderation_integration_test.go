//go:build integration

package review

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reportBy(t *testing.T, repo Repository, pool *pgxpool.Pool, reviewID uuid.UUID, n int, reason, details string) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, repo.Report(context.Background(), seedUser(t, pool), reviewID, reason, details))
	}
}

func ids(list []*Review) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(list))
	for _, r := range list {
		out = append(out, r.ID)
	}
	return out
}

// Three distinct reporters take a review out of every public read — POI
// list, global feed, someone else's view of the author's reviews, the POI's
// statistics and a direct GetReview — while its author still sees it.
// Two reporters are not enough.
func TestModeration_AutoHideAtThreeReporters_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	poiID := seedPOI(t, pool)
	author, stranger := seedUser(t, pool), seedUser(t, pool)

	flagged := &Review{UserID: author, POIID: poiID, Rating: 1, Content: "spam spam"}
	require.NoError(t, repo.Create(ctx, flagged))
	fine := &Review{UserID: seedUser(t, pool), POIID: poiID, Rating: 5, Content: "lovely"}
	require.NoError(t, repo.Create(ctx, fine))

	reportBy(t, repo, pool, flagged.ID, 2, "spam", "")
	list, total, err := repo.ListByPOI(ctx, poiID, uuid.Nil, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total, "two reports do not hide a review")
	assert.ElementsMatch(t, []uuid.UUID{flagged.ID, fine.ID}, ids(list))

	reportBy(t, repo, pool, flagged.ID, 1, "offensive", "")

	// Public reads.
	list, total, err = repo.ListByPOI(ctx, poiID, uuid.Nil, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []uuid.UUID{fine.ID}, ids(list))
	list, total, err = repo.ListByPOI(ctx, poiID, stranger, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fine.ID}, ids(list), "a signed-in stranger does not see it either")
	assert.Equal(t, 1, total)
	list, _, err = repo.ListRecent(ctx, uuid.Nil, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{fine.ID}, ids(list))
	list, total, err = repo.ListByUser(ctx, author, stranger, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, list)
	assert.Zero(t, total)
	st, err := repo.Statistics(ctx, poiID)
	require.NoError(t, err)
	assert.Equal(t, 1, st.TotalReviews, "statistics leave the hidden review out")
	assert.InDelta(t, 5.0, st.AverageRating, 1e-9)
	assert.Zero(t, st.Distribution[0])
	_, err = svc.GetReview(ctx, flagged.ID, stranger)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = svc.GetReview(ctx, flagged.ID, uuid.Nil)
	require.ErrorIs(t, err, ErrNotFound)
	us, err := svc.GetUserStatistics(ctx, author, stranger)
	require.NoError(t, err)
	assert.Zero(t, us.TotalReviews)

	// The author still sees it, marked hidden.
	list, total, err = repo.ListByPOI(ctx, poiID, author, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.ElementsMatch(t, []uuid.UUID{flagged.ID, fine.ID}, ids(list))
	list, _, err = repo.ListByUser(ctx, author, author, 10, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].Hidden)
	mine, err := svc.GetReview(ctx, flagged.ID, author)
	require.NoError(t, err)
	assert.True(t, mine.Hidden)
	mine, err = repo.GetByUserAndPOI(ctx, author, poiID)
	require.NoError(t, err)
	assert.True(t, mine.Hidden)
	us, err = svc.GetUserStatistics(ctx, author, author)
	require.NoError(t, err)
	assert.Equal(t, 1, us.TotalReviews)
}

// The moderation queue lists reviews with open reports, most-reported first,
// with counts by reason and the details reporters gave. KEEP closes the round
// and makes the review public again; reports after it open a new round.
// REMOVE hides the review for good, from the queue too; KEEP undoes it.
func TestModeration_QueueKeepAndRemove_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	poiID := seedPOI(t, pool)
	author := seedUser(t, pool)
	moderator := seedUser(t, pool)

	most := &Review{UserID: author, POIID: poiID, Rating: 1, Content: "most reported"}
	require.NoError(t, repo.Create(ctx, most))
	less := &Review{UserID: seedUser(t, pool), POIID: poiID, Rating: 2, Content: "once"}
	require.NoError(t, repo.Create(ctx, less))
	clean := &Review{UserID: seedUser(t, pool), POIID: poiID, Rating: 4, Content: "nobody minds"}
	require.NoError(t, repo.Create(ctx, clean))

	reportBy(t, repo, pool, most.ID, 2, "spam", "link farm")
	reportBy(t, repo, pool, most.ID, 1, "fake", "")
	reportBy(t, repo, pool, less.ID, 1, "other", "")

	queue, total, err := repo.ListReported(ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total, "a review nobody reported is not in the queue")
	require.Len(t, queue, 2)
	assert.Equal(t, most.ID, queue[0].Review.ID, "most-reported first")
	assert.Equal(t, 3, queue[0].OpenReports)
	assert.Equal(t, map[string]int{"spam": 2, "fake": 1}, queue[0].Reasons)
	assert.Equal(t, []string{"link farm", "link farm"}, queue[0].Details, "empty details are left out")
	assert.False(t, queue[0].LastReportedAt.IsZero())
	assert.True(t, queue[0].Review.Hidden)
	assert.Equal(t, less.ID, queue[1].Review.ID)
	assert.False(t, queue[1].Review.Hidden)

	// Paging.
	page2, total, err := repo.ListReported(ctx, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, page2, 1)
	assert.Equal(t, less.ID, page2[0].Review.ID)

	// KEEP: public again, out of the queue.
	require.NoError(t, repo.Moderate(ctx, most.ID, moderator, ModerationKeep))
	got, err := repo.GetByID(ctx, most.ID)
	require.NoError(t, err)
	assert.False(t, got.Hidden)
	assert.Equal(t, 3, got.ReportCount, "the reports themselves are kept for the record")
	queue, _, err = repo.ListReported(ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{less.ID}, []uuid.UUID{queue[0].Review.ID})
	assert.Len(t, queue, 1)

	// New reports after KEEP start a new round, and three hide it again.
	reportBy(t, repo, pool, most.ID, 2, "offensive", "")
	got, err = repo.GetByID(ctx, most.ID)
	require.NoError(t, err)
	assert.False(t, got.Hidden, "two new reports are not enough after a KEEP")
	reportBy(t, repo, pool, most.ID, 1, "offensive", "")
	got, err = repo.GetByID(ctx, most.ID)
	require.NoError(t, err)
	assert.True(t, got.Hidden)
	queue, _, err = repo.ListReported(ctx, 10, 0)
	require.NoError(t, err)
	require.Len(t, queue, 2)
	assert.Equal(t, most.ID, queue[0].Review.ID)
	assert.Equal(t, map[string]int{"offensive": 3}, queue[0].Reasons, "only the open round counts")

	// REMOVE: hidden with no open reports at all, gone from the queue, and
	// still the author's.
	require.NoError(t, repo.Moderate(ctx, less.ID, moderator, ModerationRemove))
	list, _, err := repo.ListByPOI(ctx, poiID, uuid.Nil, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{clean.ID}, ids(list))
	queue, _, err = repo.ListReported(ctx, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{most.ID}, []uuid.UUID{queue[0].Review.ID})
	assert.Len(t, queue, 1)
	theirs, _, err := repo.ListByUser(ctx, less.UserID, less.UserID, 10, 0)
	require.NoError(t, err)
	require.Len(t, theirs, 1)
	assert.True(t, theirs[0].Hidden)

	var by uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT moderated_by FROM reviews WHERE id = $1`, less.ID).Scan(&by))
	assert.Equal(t, moderator, by)

	// KEEP undoes a removal.
	require.NoError(t, repo.Moderate(ctx, less.ID, moderator, ModerationKeep))
	list, _, err = repo.ListByPOI(ctx, poiID, uuid.Nil, 10, 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{clean.ID, less.ID}, ids(list))

	require.ErrorIs(t, repo.Moderate(ctx, uuid.New(), moderator, ModerationRemove), ErrNotFound)
	require.ErrorIs(t, repo.Moderate(ctx, most.ID, moderator, ModerationAction("ban")), ErrInvalidReview)
}
