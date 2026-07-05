//go:build integration

package subscription

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func newUsageRepo(t *testing.T) (Repository, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testsupport.StartPostgres(t)
	testsupport.Truncate(t, pool, "user_daily_usage", "subscriptions", "users")
	return NewRepository(pool), pool
}

func insertUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	email := fmt.Sprintf("quota-%s@example.com", uuid.NewString())
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email) VALUES ($1) RETURNING id", email).Scan(&id)
	require.NoError(t, err, "insert test user")
	return id
}

func TestTryIncrementUsage_SequentialUpToLimit(t *testing.T) {
	repo, pool := newUsageRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	const limit = 3

	for want := 1; want <= limit; want++ {
		allowed, count, err := repo.TryIncrementUsage(ctx, userID, limit)
		require.NoError(t, err)
		require.True(t, allowed, "request %d should be allowed", want)
		require.Equal(t, want, count)
	}

	allowed, _, err := repo.TryIncrementUsage(ctx, userID, limit)
	require.NoError(t, err)
	require.False(t, allowed, "request beyond limit should be denied")

	// Denials must not inflate the stored counter.
	usage, err := repo.GetDailyUsage(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, limit, usage)
}

func TestTryIncrementUsage_ConcurrentAtLimit(t *testing.T) {
	repo, pool := newUsageRepo(t)
	ctx := context.Background()
	userID := insertUser(t, pool)
	const limit = 10
	const attempts = 25

	var allowedCount atomic.Int32
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := repo.TryIncrementUsage(ctx, userID, limit)
			require.NoError(t, err)
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int32(limit), allowedCount.Load(),
		"exactly limit requests may pass under concurrency")

	usage, err := repo.GetDailyUsage(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, limit, usage)
}

func TestGetUserPlan_DefaultsToFree(t *testing.T) {
	repo, pool := newUsageRepo(t)
	userID := insertUser(t, pool)

	plan, err := repo.GetUserPlan(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, PlanFree, plan)
}
