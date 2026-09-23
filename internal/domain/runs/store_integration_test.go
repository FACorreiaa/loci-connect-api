//go:build integration

package runs

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Run with: RUNS_TEST_DSN=postgres://... go test -tags=integration ./internal/domain/runs/

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("RUNS_TEST_DSN")
	if dsn == "" {
		t.Skip("RUNS_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, username, email, password_hash)
		VALUES ($1, $2, $3, 'x')`, id, "runner-"+id.String()[:8], id.String()+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id
}

func TestReserveStopsAtThree(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)

	for range MaxConcurrent {
		_, err := s.Reserve(ctx, user)
		require.NoError(t, err)
	}
	_, err := s.Reserve(ctx, user)
	require.ErrorIs(t, err, ErrAtCapacity)
}

// Two starts racing at two running must not both get in.
func TestReserveIsRaceSafe(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)
	for range MaxConcurrent - 1 {
		_, err := s.Reserve(ctx, user)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = s.Reserve(ctx, user) }()
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		} else {
			require.ErrorIs(t, e, ErrAtCapacity)
		}
	}
	require.Equal(t, 1, ok)
}

func TestStaleRunDoesNotCountAndReadsFailed(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)

	runID, err := s.Reserve(ctx, user)
	require.NoError(t, err)
	session := uuid.New()
	require.NoError(t, s.Attach(ctx, runID, session, "itinerary", "Crete"))
	_, err = pool.Exec(ctx, `UPDATE generation_runs SET started_at = NOW() - interval '11 minutes' WHERE id = $1`, runID)
	require.NoError(t, err)

	for range MaxConcurrent {
		_, err := s.Reserve(ctx, user)
		require.NoError(t, err)
	}
	got, err := s.Statuses(ctx, user, []uuid.UUID{session})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, StatusFailed, got[0].Status)
	require.Equal(t, ErrorCodeDeadline, got[0].ErrorCode)
}

// A row already stale must not be resurrected by a late Finish call — a
// reader who already saw it as failed/deadline_exceeded must not have that
// flip back to a real completion racing in behind them.
func TestFinishDoesNotMoveAStaleRun(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)

	runID, err := s.Reserve(ctx, user)
	require.NoError(t, err)
	session := uuid.New()
	require.NoError(t, s.Attach(ctx, runID, session, "itinerary", "Crete"))
	_, err = pool.Exec(ctx, `UPDATE generation_runs SET started_at = NOW() - interval '11 minutes' WHERE id = $1`, runID)
	require.NoError(t, err)

	_, moved, err := s.Finish(ctx, runID, StatusDone, "")
	require.NoError(t, err)
	require.False(t, moved, "a stale run must not be moved by a late Finish")

	got, err := s.Statuses(ctx, user, []uuid.UUID{session})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, StatusFailed, got[0].Status)
	require.Equal(t, ErrorCodeDeadline, got[0].ErrorCode)
}

func TestFinishMovesOnceAndClaimIsExactlyOnce(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)
	runID, _ := s.Reserve(ctx, user)
	require.NoError(t, s.Attach(ctx, runID, uuid.New(), "itinerary", "Crete"))

	run, moved, err := s.Finish(ctx, runID, StatusDone, "")
	require.NoError(t, err)
	require.True(t, moved)
	require.Equal(t, "Crete", run.CityName)
	_, moved, err = s.Finish(ctx, runID, StatusFailed, "internal")
	require.NoError(t, err)
	require.False(t, moved, "a finished run must not be re-finished")

	var wg sync.WaitGroup
	claims := make([]bool, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); claims[i], _ = s.ClaimNotification(ctx, runID) }()
	}
	wg.Wait()
	require.NotEqual(t, claims[0], claims[1], "exactly one claim wins")
}

func TestStatusesOnlyReturnsTheCallersRuns(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	alice, bob := seedUser(t, pool), seedUser(t, pool)
	runID, _ := s.Reserve(ctx, alice)
	session := uuid.New()
	require.NoError(t, s.Attach(ctx, runID, session, "itinerary", "Crete"))

	got, err := s.Statuses(ctx, bob, []uuid.UUID{session})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestReleaseOnlyDeletesUnattachedRows(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)

	loose, _ := s.Reserve(ctx, user)
	require.NoError(t, s.Release(ctx, loose))
	attached, _ := s.Reserve(ctx, user)
	session := uuid.New()
	require.NoError(t, s.Attach(ctx, attached, session, "itinerary", ""))
	require.NoError(t, s.Release(ctx, attached))

	_, found, err := s.FindBySession(ctx, user, session)
	require.NoError(t, err)
	require.True(t, found)
}

// A session has one run per turn. With an older finished turn and a newer
// running one, both readers must report the newer run.
func TestSessionWithTwoTurnsReportsTheNewest(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool)
	ctx := context.Background()
	user := seedUser(t, pool)
	session := uuid.New()

	first, err := s.Reserve(ctx, user)
	require.NoError(t, err)
	require.NoError(t, s.Attach(ctx, first, session, "itinerary", "Crete"))
	_, moved, err := s.Finish(ctx, first, StatusDone, "")
	require.NoError(t, err)
	require.True(t, moved)
	_, err = pool.Exec(ctx, `UPDATE generation_runs SET started_at = NOW() - interval '1 minute' WHERE id = $1`, first)
	require.NoError(t, err)

	second, err := s.Reserve(ctx, user)
	require.NoError(t, err)
	require.NoError(t, s.Attach(ctx, second, session, "itinerary", "Crete"),
		"a second turn on the same session must attach")

	run, found, err := s.FindBySession(ctx, user, session)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, second, run.ID)
	require.Equal(t, StatusRunning, run.Status)

	statuses, err := s.Statuses(ctx, user, []uuid.UUID{session})
	require.NoError(t, err)
	require.Len(t, statuses, 1, "one row per session, not one per turn")
	require.Equal(t, second, statuses[0].ID)
	require.Equal(t, StatusRunning, statuses[0].Status)
}
