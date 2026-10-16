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
