//go:build integration

package watch

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	chatrepo "github.com/FACorreiaa/loci-connect-api/internal/domain/chat/repository"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

// Run with: WATCH_TEST_DSN=postgres://... go test -tags=integration ./internal/domain/watch/
// The database needs the image the migrations expect (PostGIS + TimescaleDB +
// pgvector, e.g. timescale/timescaledb-ha:pg17); migrations are applied here.

var migrateOnce sync.Once

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("WATCH_TEST_DSN")
	if dsn == "" {
		t.Skip("WATCH_TEST_DSN not set")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var migErr error
	migrateOnce.Do(func() {
		d, err := db.New(db.Config{DSN: dsn, MaxConns: 4, MinConns: 1, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute}, logger)
		if err != nil {
			migErr = err
			return
		}
		defer d.Close()
		migErr = d.RunMigrations()
	})
	require.NoError(t, migErr)
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func seedUserAndSession(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	user := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO users (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')`,
		user, "watch-"+user.String()[:8], user.String()+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() {
		// chat_sessions carries a second, non-cascading FK to users, so the
		// threads go first; their watches cascade with them.
		_, _ = pool.Exec(context.Background(), `DELETE FROM chat_sessions WHERE user_id = $1`, user)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user)
	})

	session := uuid.New()
	now := time.Now()
	require.NoError(t, chatrepo.NewRepositoryImpl(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).CreateSession(ctx, locitypes.ChatSession{
		ID: session, UserID: user, CityName: "Lisbon",
		ConversationHistory: []locitypes.ConversationMessage{},
		CreatedAt:           now, UpdatedAt: now, ExpiresAt: now.Add(24 * time.Hour), Status: locitypes.StatusActive,
	}))
	return user, session
}

func TestRepositoryLifecycle(t *testing.T) {
	pool := testPool(t)
	repo := NewPostgresRepository(pool)
	ctx := context.Background()
	user, session := seedUserAndSession(t, pool)

	now := time.Now().UTC().Truncate(time.Second)
	due, err := repo.Create(ctx, Watch{UserID: user, SessionID: session, Title: "Due", ScheduleHuman: "Every hour", IntervalMinutes: 60, Spec: "s", NextRunAt: now.Add(-3*time.Hour - time.Minute)})
	require.NoError(t, err)
	require.True(t, due.Enabled)
	later, err := repo.Create(ctx, Watch{UserID: user, SessionID: session, Title: "Later", ScheduleHuman: "Every day at 08:00", IntervalMinutes: 1440, Spec: "s", NextRunAt: now.Add(time.Hour)})
	require.NoError(t, err)

	n, err := repo.CountByUser(ctx, user)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	all, err := repo.List(ctx, user, nil)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{due.ID, later.ID}, []uuid.UUID{all[0].ID, all[1].ID}, "soonest first")
	other := uuid.New()
	none, err := repo.List(ctx, user, &other)
	require.NoError(t, err)
	require.Empty(t, none)

	// Claim: only the due one, advanced past now with missed slots skipped.
	// Other tests' rows may share the table, so look only at ours.
	claimed, err := repo.ClaimDue(ctx, now, 100)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{due.ID}, ownIDs(claimed, due.ID, later.ID))
	again, err := repo.ClaimDue(ctx, now, 100)
	require.NoError(t, err)
	require.Empty(t, ownIDs(again, due.ID, later.ID), "a claimed slot is not handed out twice")

	all, err = repo.List(ctx, user, &session)
	require.NoError(t, err)
	var advanced Watch
	for _, w := range all {
		if w.ID == due.ID {
			advanced = w
		}
	}
	require.True(t, advanced.NextRunAt.After(now))
	require.False(t, advanced.NextRunAt.After(now.Add(time.Hour)))
	require.NotNil(t, advanced.LastRunAt)

	require.ErrorIs(t, repo.Delete(ctx, uuid.New(), later.ID), ErrNotFound, "not the owner")
	require.NoError(t, repo.Delete(ctx, user, later.ID))

	require.NoError(t, repo.Disable(ctx, due.ID))
	claimed, err = repo.ClaimDue(ctx, now.Add(48*time.Hour), 100)
	require.NoError(t, err)
	require.Empty(t, ownIDs(claimed, due.ID), "disabled watches never run")

	// Deleting the thread deletes its watches.
	_, err = pool.Exec(ctx, `DELETE FROM chat_sessions WHERE id = $1`, session)
	require.NoError(t, err)
	n, err = repo.CountByUser(ctx, user)
	require.NoError(t, err)
	require.Zero(t, n)
}

func ownIDs(ws []Watch, mine ...uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, w := range ws {
		for _, id := range mine {
			if w.ID == id {
				out = append(out, id)
			}
		}
	}
	return out
}

func TestPgLockerIsExclusiveAcrossSessions(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	a, b := NewPgLocker(pool), NewPgLocker(pool)

	unlock, err := a.TryLock(ctx)
	require.NoError(t, err)

	_, err = b.TryLock(ctx)
	require.ErrorIs(t, err, ErrLockHeld, "a second replica must step aside")

	unlock()
	unlockB, err := b.TryLock(ctx)
	require.NoError(t, err, "released lock is free again")
	unlockB()
}

// End to end against a real thread: a due watch posts a proactive message
// that GetSession (what GetChatSession serves) returns.
func TestRunnerPostsIntoRealThread(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	user, session := seedUserAndSession(t, pool)

	repo := NewPostgresRepository(pool)
	chat := chatrepo.NewRepositoryImpl(pool, logger)
	_, err := repo.Create(ctx, Watch{UserID: user, SessionID: session, Title: "Rain", ScheduleHuman: "Every hour", IntervalMinutes: 60, Spec: "will it rain", NextRunAt: time.Now().Add(-time.Minute)})
	require.NoError(t, err)

	svc := NewService(repo, chat, &fakeGen{reply: "No rain expected today."}, logger)
	require.True(t, NewRunner(svc, NewPgLocker(pool), logger).Tick(ctx))

	got, err := chat.GetSession(ctx, session)
	require.NoError(t, err)
	require.Len(t, got.ConversationHistory, 1)
	msg := got.ConversationHistory[0]
	require.Equal(t, "No rain expected today.", msg.Content)
	require.Equal(t, locitypes.OriginProactive, msg.Origin)
	require.Equal(t, SourceLabel, msg.SourceLabel)
}
