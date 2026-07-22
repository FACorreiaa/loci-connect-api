package preference

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VectorReader loads a stored user preference embedding.
type VectorReader interface {
	GetEmbedding(ctx context.Context, userID uuid.UUID) ([]float32, bool, error)
}

// VectorStore reads and writes user preference vectors.
type VectorStore interface {
	VectorReader
	Upsert(ctx context.Context, userID uuid.UUID, embedding []float32, feedbackCount int, lastFeedbackAt *time.Time) error
}

type store struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

// NewVectorStore persists preference vectors in user_preference_vectors.
func NewVectorStore(db *pgxpool.Pool, logger *slog.Logger) VectorStore {
	if db == nil {
		return noopStore{}
	}
	l := logger
	if l == nil {
		l = slog.Default()
	}
	return &store{db: db, logger: l.With(slog.String("component", "preference-vectors"))}
}

func (s *store) GetEmbedding(ctx context.Context, userID uuid.UUID) ([]float32, bool, error) {
	if userID == uuid.Nil {
		return nil, false, nil
	}
	var raw string
	err := s.db.QueryRow(ctx, `
		SELECT vectors.embedding::text
		FROM user_preference_vectors vectors
		LEFT JOIN personalization_settings settings ON settings.user_id = vectors.user_id
		WHERE vectors.user_id = $1
		  AND COALESCE(settings.personalization_enabled, TRUE)`, userID).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get preference vector: %w", err)
	}
	v, err := ParseVector(raw)
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (s *store) Upsert(ctx context.Context, userID uuid.UUID, embedding []float32, feedbackCount int, lastFeedbackAt *time.Time) error {
	if userID == uuid.Nil || len(embedding) == 0 {
		return fmt.Errorf("invalid upsert args")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_preference_vectors (user_id, embedding, feedback_count, last_feedback_at, updated_at)
		VALUES ($1, $2::vector, $3, $4, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			embedding = EXCLUDED.embedding,
			feedback_count = EXCLUDED.feedback_count,
			last_feedback_at = EXCLUDED.last_feedback_at,
			updated_at = NOW()`,
		userID, FormatVector(embedding), feedbackCount, lastFeedbackAt)
	if err != nil {
		return fmt.Errorf("upsert preference vector: %w", err)
	}
	return nil
}

type noopStore struct{}

func (noopStore) GetEmbedding(context.Context, uuid.UUID) ([]float32, bool, error) {
	return nil, false, nil
}

func (noopStore) Upsert(context.Context, uuid.UUID, []float32, int, *time.Time) error {
	return nil
}
