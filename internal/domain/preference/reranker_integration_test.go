//go:build integration

package preference

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

type capturingStore struct {
	embedding []float32
}

func (s *capturingStore) GetEmbedding(context.Context, uuid.UUID) ([]float32, bool, error) {
	return s.embedding, s.embedding != nil, nil
}

func (s *capturingStore) Upsert(_ context.Context, _ uuid.UUID, embedding []float32, _ int, _ *time.Time) error {
	s.embedding = embedding
	return nil
}

// unitVector is a 768-dim one-hot vector in pgvector text form.
func unitVector(axis int) string {
	parts := make([]string, 768)
	for i := range parts {
		parts[i] = "0"
	}
	parts[axis] = "1"
	return "[" + strings.Join(parts, ",") + "]"
}

// A dismissal recorded the way RecordEvents stores it (a positive magnitude on
// a skipped event) must pull the taste vector away from the dismissed place.
func TestRecomputeUser_DismissedPlacePushesAway(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()

	userID := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)",
		userID, "rerank-"+uuid.NewString()+"@loci.test")
	require.NoError(t, err)

	insertPOI := func(name string, axis int) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO points_of_interest (name, location, embedding)
			VALUES ($1, ST_SetSRID(ST_MakePoint(-9.14, 38.71), 4326), $2::vector)
			RETURNING id`, fmt.Sprintf("%s-%s", name, uuid.NewString()), unitVector(axis)).Scan(&id))
		return id
	}
	liked := insertPOI("Liked", 0)
	dismissed := insertPOI("Dismissed", 1)

	_, err = pool.Exec(ctx, `
		INSERT INTO preference_feedback (user_id, poi_id, event, weight)
		VALUES ($1, $2, 'favorited', 1), ($1, $3, 'skipped', 0.5)`,
		userID, liked.String(), dismissed.String())
	require.NoError(t, err)

	store := &capturingStore{}
	r := NewReranker(pool, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, r.RecomputeUser(ctx, userID))

	require.NotNil(t, store.embedding)
	require.Greater(t, store.embedding[0], float32(0), "liked axis should be positive")
	require.Less(t, store.embedding[1], float32(0), "dismissed axis should be negative")
}

func newTestUser(ctx context.Context, t *testing.T, prefix string) uuid.UUID {
	t.Helper()
	pool := testsupport.MustPool()
	id := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)",
		id, prefix+"-"+uuid.NewString()+"@loci.test")
	require.NoError(t, err)
	return id
}

func feedbackRows(ctx context.Context, t *testing.T, userID uuid.UUID) []struct {
	Event  string
	Weight float32
} {
	t.Helper()
	rows, err := testsupport.MustPool().Query(ctx,
		`SELECT event, weight FROM preference_feedback WHERE user_id = $1 ORDER BY created_at`, userID)
	require.NoError(t, err)
	defer rows.Close()
	var out []struct {
		Event  string
		Weight float32
	}
	for rows.Next() {
		var r struct {
			Event  string
			Weight float32
		}
		require.NoError(t, rows.Scan(&r.Event, &r.Weight))
		out = append(out, r)
	}
	return out
}

// Editing a review from 5 to 1 stars must leave one signal for that review,
// the new one, rather than a positive and a negative that cancel out.
func TestRecorderReplace_SwapsSignalForKey(t *testing.T) {
	ctx := context.Background()
	userID := newTestUser(ctx, t, "replace")
	rec := NewRecorder(testsupport.MustPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	poi := uuid.NewString()
	rec.Replace(ctx, userID, "review_id", "r1", EventVisited, RecordOpts{POIID: poi, Weight: 1, Metadata: map[string]any{"review_id": "r1"}})
	rec.Replace(ctx, userID, "review_id", "r2", EventVisited, RecordOpts{POIID: poi, Weight: 0.7, Metadata: map[string]any{"review_id": "r2"}})
	rec.Replace(ctx, userID, "review_id", "r1", EventSkipped, RecordOpts{POIID: poi, Weight: 1, Metadata: map[string]any{"review_id": "r1"}})

	rows := feedbackRows(ctx, t, userID)
	require.Len(t, rows, 2)
	require.Equal(t, EventVisited, rows[0].Event, "r2 untouched")
	require.Equal(t, EventSkipped, rows[1].Event, "r1 replaced")
}

// A traveller who switched personalization off is not learned from, whichever
// domain records the signal.
func TestRecorder_RespectsPersonalizationOff(t *testing.T) {
	ctx := context.Background()
	userID := newTestUser(ctx, t, "optout")
	_, err := testsupport.MustPool().Exec(ctx, `
		INSERT INTO personalization_settings (user_id, personalization_enabled) VALUES ($1, FALSE)`, userID)
	require.NoError(t, err)
	rec := NewRecorder(testsupport.MustPool(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec.Record(ctx, userID, EventFavorited, RecordOpts{POIID: uuid.NewString()})
	rec.Replace(ctx, userID, "review_id", "r1", EventVisited, RecordOpts{POIID: uuid.NewString(), Metadata: map[string]any{"review_id": "r1"}})
	require.Empty(t, feedbackRows(ctx, t, userID))
}
