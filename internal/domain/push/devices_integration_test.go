//go:build integration

package push

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Run with: PUSH_TEST_DSN=postgres://... go test -tags=integration ./internal/domain/push/

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PUSH_TEST_DSN")
	if dsn == "" {
		t.Skip("PUSH_TEST_DSN not set")
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
		VALUES ($1, $2, $3, 'x')`, id, "pushuser-"+id.String()[:8], id.String()+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id
}

func TestUpsertRemoveAndRemoveEndpoint(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresDeviceStore(pool)
	ctx := context.Background()

	userA := seedUser(t, pool)
	userB := seedUser(t, pool)
	endpoint := "https://push.example.test/" + uuid.New().String()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM push_devices WHERE endpoint = $1`, endpoint)
	})

	// Upsert twice with the same endpoint for user A, then once for user B.
	require.NoError(t, s.Upsert(ctx, Device{UserID: userA, Platform: "web_push", Endpoint: endpoint, P256dh: "p256dh-a", Auth: "auth-a"}, "agent-a"))
	require.NoError(t, s.Upsert(ctx, Device{UserID: userA, Platform: "web_push", Endpoint: endpoint, P256dh: "p256dh-a2", Auth: "auth-a2"}, "agent-a2"))
	require.NoError(t, s.Upsert(ctx, Device{UserID: userB, Platform: "web_push", Endpoint: endpoint, P256dh: "p256dh-b", Auth: "auth-b"}, "agent-b"))

	devicesA, err := s.ForUser(ctx, userA, "web_push")
	require.NoError(t, err)
	require.Empty(t, devicesA, "endpoint moved to user B, so user A should have none")

	devicesB, err := s.ForUser(ctx, userB, "web_push")
	require.NoError(t, err)
	require.Len(t, devicesB, 1)
	require.Equal(t, endpoint, devicesB[0].Endpoint)
	require.Equal(t, "p256dh-b", devicesB[0].P256dh)
	require.Equal(t, "auth-b", devicesB[0].Auth)

	// Remove(B, endpoint) empties it.
	require.NoError(t, s.Remove(ctx, userB, endpoint))
	devicesB, err = s.ForUser(ctx, userB, "web_push")
	require.NoError(t, err)
	require.Empty(t, devicesB)

	// RemoveEndpoint removes regardless of user.
	require.NoError(t, s.Upsert(ctx, Device{UserID: userA, Platform: "web_push", Endpoint: endpoint, P256dh: "p256dh-a3", Auth: "auth-a3"}, "agent-a3"))
	devicesA, err = s.ForUser(ctx, userA, "web_push")
	require.NoError(t, err)
	require.Len(t, devicesA, 1)

	require.NoError(t, s.RemoveEndpoint(ctx, endpoint))
	devicesA, err = s.ForUser(ctx, userA, "web_push")
	require.NoError(t, err)
	require.Empty(t, devicesA)
}

// An endpoint registered on one platform cannot be taken over by a
// registration for another: the conflicting upsert changes nothing.
func TestUpsertDoesNotCrossPlatforms(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresDeviceStore(pool)
	ctx := context.Background()

	userA := seedUser(t, pool)
	userB := seedUser(t, pool)
	endpoint := "https://push.example.test/" + uuid.New().String()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM push_devices WHERE endpoint = $1`, endpoint)
	})

	require.NoError(t, s.Upsert(ctx, Device{UserID: userA, Platform: "web_push", Endpoint: endpoint, P256dh: "p256dh-a", Auth: "auth-a"}, "agent-a"))
	require.NoError(t, s.Upsert(ctx, userB, "apns", endpoint, "", "", "agent-b"))

	devicesA, err := s.ForUser(ctx, userA, "web_push")
	require.NoError(t, err)
	require.Len(t, devicesA, 1, "the web_push row still belongs to A")
	require.Equal(t, "p256dh-a", devicesA[0].P256dh)
	require.Equal(t, "auth-a", devicesA[0].Auth)

	devicesB, err := s.ForUser(ctx, userB, "apns")
	require.NoError(t, err)
	require.Empty(t, devicesB)
}
