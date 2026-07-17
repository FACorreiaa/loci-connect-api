package subscription

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	// TryIncrementUsage atomically consumes one request if the day's count is
	// below limit. Returns allowed=false (count 0) when the quota is spent;
	// denied attempts do not inflate the counter.
	TryIncrementUsage(ctx context.Context, userID uuid.UUID, limit int) (allowed bool, count int, err error)
	GetUserPlan(ctx context.Context, userID uuid.UUID) (string, error)
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

func (r *repository) GetUserPlan(ctx context.Context, userID uuid.UUID) (string, error) {
	var plan, status string
	var endDate *time.Time
	query := `
		SELECT plan::text, status::text, end_date
		FROM subscriptions
		WHERE user_id = $1
	`
	err := r.pgpool.QueryRow(ctx, query, userID).Scan(&plan, &status, &endDate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlanFree, nil
		}
		return "", fmt.Errorf("failed to get user plan: %w", err)
	}
	return effectivePlan(plan, status, endDate), nil
}

func (r *repository) TryIncrementUsage(ctx context.Context, userID uuid.UUID, limit int) (bool, int, error) {
	// Single round trip, race-free: the conditional upsert only increments
	// while under the limit; a conflicting row at/over the limit matches no
	// rows, so denials never inflate the counter.
	query := `
		INSERT INTO user_daily_usage (user_id, usage_date, request_count)
		VALUES ($1, CURRENT_DATE, 1)
		ON CONFLICT (user_id, usage_date)
		DO UPDATE SET request_count = user_daily_usage.request_count + 1, updated_at = NOW()
		WHERE user_daily_usage.request_count < $2
		RETURNING request_count
	`
	var count int
	err := r.pgpool.QueryRow(ctx, query, userID, limit).Scan(&count)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("failed to consume daily quota: %w", err)
	}
	return true, count, nil
}
