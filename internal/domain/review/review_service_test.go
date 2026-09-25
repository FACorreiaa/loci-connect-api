package review

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo answers GetByID from a map and records helpful votes; every other
// method panics through the embedded interface, which is the point.
type fakeRepo struct {
	Repository
	reviews map[uuid.UUID]*Review
	voted   []uuid.UUID
}

func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Review, error) {
	if r, ok := f.reviews[id]; ok {
		return r, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) SetHelpful(_ context.Context, _, reviewID uuid.UUID, _ bool) (int, error) {
	f.voted = append(f.voted, reviewID)
	return 1, nil
}

// A helpful vote on your own review is refused before anything is written;
// anyone else's vote goes through as before.
func TestService_LikeReview_RejectsOwnReview(t *testing.T) {
	author, other := uuid.New(), uuid.New()
	review := &Review{ID: uuid.New(), UserID: author}
	repo := &fakeRepo{reviews: map[uuid.UUID]*Review{review.ID: review}}
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := svc.LikeReview(context.Background(), author, review.ID, true)
	require.ErrorIs(t, err, ErrOwnReview)
	assert.Empty(t, repo.voted, "nothing is written for a refused vote")

	count, err := svc.LikeReview(context.Background(), other, review.ID, true)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, []uuid.UUID{review.ID}, repo.voted)

	_, err = svc.LikeReview(context.Background(), other, uuid.New(), true)
	require.ErrorIs(t, err, ErrNotFound)
}
