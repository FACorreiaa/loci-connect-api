package preference

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RerankStats summarizes a job run.
type RerankStats struct {
	UsersConsidered int
	UsersUpdated    int
	UsersSkipped    int
	SignalsUsed     int
}

// Reranker aggregates preference_feedback into user_preference_vectors.
type Reranker struct {
	db     *pgxpool.Pool
	store  VectorStore
	logger *slog.Logger
}

const rerankerAdvisoryLockID int64 = 0x4c4f4349 // "LOCI"

// NewReranker builds a preference re-rank job.
func NewReranker(db *pgxpool.Pool, store VectorStore, logger *slog.Logger) *Reranker {
	l := logger
	if l == nil {
		l = slog.Default()
	}
	return &Reranker{
		db:     db,
		store:  store,
		logger: l.With(slog.String("component", "preference-rerank")),
	}
}

// Run recomputes preference vectors for users with feedback since lookback,
// or all users with any feedback when lookback <= 0.
func (r *Reranker) Run(ctx context.Context, lookback time.Duration) (RerankStats, error) {
	var stats RerankStats
	if r.db == nil || r.store == nil {
		return stats, fmt.Errorf("reranker not configured")
	}
	lockConn, err := r.db.Acquire(ctx)
	if err != nil {
		return stats, fmt.Errorf("acquire reranker lock connection: %w", err)
	}
	defer lockConn.Release()
	var locked bool
	if err := lockConn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, rerankerAdvisoryLockID).Scan(&locked); err != nil {
		return stats, fmt.Errorf("acquire reranker advisory lock: %w", err)
	}
	if !locked {
		r.logger.InfoContext(ctx, "preference rerank already running; skipping overlapping run")
		return stats, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := lockConn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, rerankerAdvisoryLockID); err != nil {
			r.logger.WarnContext(unlockCtx, "failed to release reranker advisory lock", slog.Any("error", err))
		}
	}()

	users, err := r.usersWithFeedback(ctx, lookback)
	if err != nil {
		return stats, err
	}
	stats.UsersConsidered = len(users)

	for _, uid := range users {
		n, updated, err := r.recomputeUser(ctx, uid)
		if err != nil {
			r.logger.WarnContext(ctx, "preference recompute failed",
				slog.String("user_id", uid.String()),
				slog.Any("error", err))
			stats.UsersSkipped++
			continue
		}
		stats.SignalsUsed += n
		if updated {
			stats.UsersUpdated++
		} else {
			stats.UsersSkipped++
		}
	}
	return stats, nil
}

func (r *Reranker) usersWithFeedback(ctx context.Context, lookback time.Duration) ([]uuid.UUID, error) {
	query := `
		SELECT DISTINCT feedback.user_id
		FROM preference_feedback feedback
		LEFT JOIN personalization_settings settings ON settings.user_id = feedback.user_id
		WHERE COALESCE(settings.personalization_enabled, TRUE)`
	var args []any
	if lookback > 0 {
		query += ` AND feedback.created_at >= $1`
		args = append(args, time.Now().UTC().Add(-lookback))
	}
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list feedback users: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Reranker) recomputeUser(ctx context.Context, userID uuid.UUID) (signals int, updated bool, err error) {
	vectors, weights, count, lastAt, err := r.loadWeightedEmbeddings(ctx, userID)
	if err != nil {
		return 0, false, err
	}
	if len(vectors) == 0 {
		return 0, false, nil
	}
	avg, err := WeightedAverage(vectors, weights)
	if err != nil {
		return len(vectors), false, err
	}
	if err := r.store.Upsert(ctx, userID, avg, count, lastAt); err != nil {
		return len(vectors), false, err
	}
	if err := r.updateTasteTraits(ctx, userID); err != nil {
		return len(vectors), false, err
	}
	return len(vectors), true, nil
}

func (r *Reranker) updateTasteTraits(ctx context.Context, userID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin taste trait update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `DELETE FROM user_taste_traits WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear taste traits: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO user_taste_traits (user_id, trait_key, label, score, confidence, evidence_count, updated_at)
		SELECT
			pf.user_id,
			LOWER(COALESCE(NULLIF(p.category, ''), NULLIF(p.poi_type, ''), 'places')),
			INITCAP(COALESCE(NULLIF(p.category, ''), NULLIF(p.poi_type, ''), 'places')),
			GREATEST(-1, LEAST(1, SUM(pf.weight * CASE WHEN pf.event = 'skipped' THEN -1 ELSE 1 END) / GREATEST(COUNT(*), 1)))::double precision,
			LEAST(1, COUNT(*)::double precision / 10),
			COUNT(*)::integer,
			NOW()
		FROM preference_feedback pf
		JOIN points_of_interest p ON p.id::text = pf.poi_id
		WHERE pf.user_id = $1 AND pf.poi_id IS NOT NULL AND pf.poi_id <> ''
		GROUP BY pf.user_id, LOWER(COALESCE(NULLIF(p.category, ''), NULLIF(p.poi_type, ''), 'places')),
			INITCAP(COALESCE(NULLIF(p.category, ''), NULLIF(p.poi_type, ''), 'places'))
		HAVING COUNT(*) >= 2`, userID)
	if err != nil {
		return fmt.Errorf("rebuild taste traits: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit taste traits: %w", err)
	}
	return nil
}

func (r *Reranker) loadWeightedEmbeddings(ctx context.Context, userID uuid.UUID) (
	vectors [][]float32, weights []float32, feedbackCount int, lastAt *time.Time, err error,
) {
	// Direct POI UUID matches on preference_feedback.poi_id.
	rows, err := r.db.Query(ctx, `
		SELECT pf.event, pf.weight, p.embedding::text, pf.created_at
		FROM preference_feedback pf
		JOIN points_of_interest p ON p.id::text = pf.poi_id
		WHERE pf.user_id = $1
		  AND pf.poi_id IS NOT NULL
		  AND pf.poi_id <> ''
		  AND p.embedding IS NOT NULL`, userID)
	if err != nil {
		return nil, nil, 0, nil, fmt.Errorf("load feedback embeddings: %w", err)
	}
	defer rows.Close()

	var latest time.Time
	for rows.Next() {
		var (
			event  string
			weight float32
			raw    string
			at     time.Time
		)
		if err := rows.Scan(&event, &weight, &raw, &at); err != nil {
			return nil, nil, 0, nil, err
		}
		emb, perr := ParseVector(raw)
		if perr != nil {
			continue
		}
		vectors = append(vectors, emb)
		weights = append(weights, EventMultiplier(event)*weight)
		feedbackCount++
		if at.After(latest) {
			latest = at
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, nil, err
	}

	// Trip-linked events (reorder/export) often omit poi_id — pull stop POIs.
	tripRows, err := r.db.Query(ctx, `
		SELECT pf.event, pf.weight, p.embedding::text, pf.created_at
		FROM preference_feedback pf
		JOIN trip_days td ON td.trip_id = pf.trip_id
		JOIN trip_stops ts ON ts.day_id = td.id
		JOIN points_of_interest p ON p.id::text = ts.poi_id
		WHERE pf.user_id = $1
		  AND pf.trip_id IS NOT NULL
		  AND pf.event IN ('reordered', 'exported')
		  AND (pf.poi_id IS NULL OR pf.poi_id = '')
		  AND ts.poi_id <> ''
		  AND p.embedding IS NOT NULL`, userID)
	if err != nil {
		return nil, nil, 0, nil, fmt.Errorf("load trip feedback embeddings: %w", err)
	}
	defer tripRows.Close()

	for tripRows.Next() {
		var (
			event  string
			weight float32
			raw    string
			at     time.Time
		)
		if err := tripRows.Scan(&event, &weight, &raw, &at); err != nil {
			return nil, nil, 0, nil, err
		}
		emb, perr := ParseVector(raw)
		if perr != nil {
			continue
		}
		vectors = append(vectors, emb)
		weights = append(weights, EventMultiplier(event)*weight)
		feedbackCount++
		if at.After(latest) {
			latest = at
		}
	}
	if err := tripRows.Err(); err != nil {
		return nil, nil, 0, nil, err
	}

	if !latest.IsZero() {
		lastAt = &latest
	}
	return vectors, weights, feedbackCount, lastAt, nil
}
