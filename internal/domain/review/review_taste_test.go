package review

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
)

// tasteRepo stores reviews in memory for the create/update/delete paths.
type tasteRepo struct {
	Repository
	reviews map[uuid.UUID]*Review
}

func (f *tasteRepo) Create(_ context.Context, r *Review) error {
	r.ID = uuid.New()
	f.reviews[r.ID] = r
	return nil
}

func (f *tasteRepo) GetByID(_ context.Context, id uuid.UUID) (*Review, error) {
	if r, ok := f.reviews[id]; ok {
		return r, nil
	}
	return nil, ErrNotFound
}

func (f *tasteRepo) Update(_ context.Context, id, _ uuid.UUID, u UpdateFields) error {
	r, ok := f.reviews[id]
	if !ok {
		return ErrNotFound
	}
	r.Rating = u.Rating
	return nil
}

func (f *tasteRepo) Delete(_ context.Context, id, _ uuid.UUID) error {
	delete(f.reviews, id)
	return nil
}

// tasteLog keeps one current signal per review_id, the way Replace does in
// Postgres, so tests assert on what the reranker would see.
type tasteLog struct {
	byReview map[string]signal
	kicks    int
}

type signal struct {
	event  string
	weight float32
	poiID  string
}

func (l *tasteLog) Record(context.Context, uuid.UUID, string, preference.RecordOpts) {
	panic("reviews must use Replace so an edit swaps the signal")
}

func (l *tasteLog) Replace(_ context.Context, _ uuid.UUID, key, value, event string, opts preference.RecordOpts) {
	if key != "review_id" || opts.Metadata[key] != value {
		panic("review signal must be keyed by review_id and carry it in metadata")
	}
	l.byReview[value] = signal{event: event, weight: opts.Weight, poiID: opts.POIID}
}

func (l *tasteLog) Forget(_ context.Context, _ uuid.UUID, key, value string) {
	if key == "review_id" {
		delete(l.byReview, value)
	}
}

func (l *tasteLog) Kick(uuid.UUID) { l.kicks++ }

func newTasteService() (Service, *tasteLog) {
	log := &tasteLog{byReview: map[string]signal{}}
	svc := NewService(&tasteRepo{reviews: map[uuid.UUID]*Review{}},
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithTaste(log, log))
	return svc, log
}

func TestCreateReview_RecordsRatingSignal(t *testing.T) {
	svc, log := newTasteService()
	poi := uuid.New()
	r, err := svc.CreateReview(context.Background(), CreateReviewInput{
		UserID: uuid.New(), POIID: poi, Rating: 2, Content: "Overpriced and loud",
	})
	require.NoError(t, err)

	got, ok := log.byReview[r.ID.String()]
	require.True(t, ok)
	assert.Equal(t, signal{event: preference.EventSkipped, weight: 0.5, poiID: poi.String()}, got)
	assert.Equal(t, 1, log.kicks)
}

func TestUpdateReview_ReplacesSignal(t *testing.T) {
	svc, log := newTasteService()
	user := uuid.New()
	r, err := svc.CreateReview(context.Background(), CreateReviewInput{
		UserID: user, POIID: uuid.New(), Rating: 5, Content: "Loved it",
	})
	require.NoError(t, err)

	_, err = svc.UpdateReview(context.Background(), UpdateReviewInput{
		ReviewID: r.ID, UserID: user, Rating: 1, Content: "Went back, awful",
	})
	require.NoError(t, err)

	require.Len(t, log.byReview, 1)
	got := log.byReview[r.ID.String()]
	assert.Equal(t, preference.EventSkipped, got.event)
	assert.Equal(t, float32(1.0), got.weight)
	assert.Equal(t, 2, log.kicks)
}

func TestDeleteReview_ForgetsSignal(t *testing.T) {
	svc, log := newTasteService()
	user := uuid.New()
	r, err := svc.CreateReview(context.Background(), CreateReviewInput{
		UserID: user, POIID: uuid.New(), Rating: 1, Content: "No",
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteReview(context.Background(), r.ID, user))
	assert.Empty(t, log.byReview)
	assert.Equal(t, 2, log.kicks)
}

// Without WithTaste the service behaves exactly as before.
func TestCreateReview_NoTasteConfigured(t *testing.T) {
	svc := NewService(&tasteRepo{reviews: map[uuid.UUID]*Review{}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := svc.CreateReview(context.Background(), CreateReviewInput{
		UserID: uuid.New(), POIID: uuid.New(), Rating: 4, Content: "Good",
	})
	require.NoError(t, err)
}
