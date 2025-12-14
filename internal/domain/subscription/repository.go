package subscription

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxPool abstracts the subset of pgxpool.Pool used by the repository to allow mocking in tests.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ PgxPool = (*pgxpool.Pool)(nil)

type Repository interface {
	GetDailyUsage(ctx context.Context, userID uuid.UUID) (int, error)
	IncrementUsage(ctx context.Context, userID uuid.UUID) error
}

type repository struct {
	pgpool PgxPool
}

func NewRepository(pgpool PgxPool) Repository {
	return &repository{pgpool: pgpool}
}

func (r *repository) GetDailyUsage(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	query := `
		SELECT request_count 
		FROM user_daily_usage 
		WHERE user_id = $1 AND usage_date = CURRENT_DATE
	`
	err := r.pgpool.QueryRow(ctx, query, userID).Scan(&count)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get daily usage: %w", err)
	}
	return count, nil
}

func (r *repository) IncrementUsage(ctx context.Context, userID uuid.UUID) error {
	query := `
		INSERT INTO user_daily_usage (user_id, usage_date, request_count)
		VALUES ($1, CURRENT_DATE, 1)
		ON CONFLICT (user_id, usage_date) 
		DO UPDATE SET request_count = user_daily_usage.request_count + 1, updated_at = NOW()
	`
	_, err := r.pgpool.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to increment usage: %w", err)
	}
	return nil
}
