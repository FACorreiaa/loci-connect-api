//go:build integration || e2e

// Package testsupport provides shared helpers for integration and e2e tests,
// notably an ephemeral Postgres container (PostGIS + TimescaleDB + pgvector)
// with the application's real migrations applied.
package testsupport

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

const (
	testDBUser = "postgres"
	testDBPass = "postgres"
	testDBName = "loci_test"

	// defaultImage is a multi-arch (amd64 + arm64) image bundling PostgreSQL,
	// PostGIS, pgvector and TimescaleDB — the extensions the migrations require.
	// The production image (postgis/postgis, built via postgres.Dockerfile) is
	// amd64-only, so it cannot build on Apple Silicon; this prebuilt image runs
	// everywhere and avoids a multi-minute source compile. Override with
	// TEST_POSTGRES_IMAGE if a different version is needed.
	defaultImage = "timescale/timescaledb-ha:pg17"
)

func image() string {
	if v := os.Getenv("TEST_POSTGRES_IMAGE"); v != "" {
		return v
	}
	return defaultImage
}

var (
	startOnce  sync.Once
	sharedPool *pgxpool.Pool
	sharedDSN  string
	sharedHost string
	sharedPort int
	startErr   error
)

// StartPostgres returns a connection pool to a shared, migrated Postgres
// container. The container is built and started once per test process (the
// PostGIS/TimescaleDB/pgvector image is expensive to build); subsequent calls
// reuse it. The container is reaped automatically by Ryuk when the process
// exits.
func StartPostgres(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	startOnce.Do(func() {
		sharedPool, sharedDSN, startErr = startContainer()
	})
	if startErr != nil {
		t.Fatalf("testsupport: failed to start postgres container: %v", startErr)
	}
	return sharedPool, sharedDSN
}

// MustPool starts (or reuses) the shared, migrated container and returns its
// pool. Intended for use from TestMain (which has no *testing.T); it aborts the
// process on failure.
func MustPool() *pgxpool.Pool {
	startOnce.Do(func() {
		sharedPool, sharedDSN, startErr = startContainer()
	})
	if startErr != nil {
		log.Fatalf("testsupport: failed to start postgres container: %v", startErr)
	}
	return sharedPool
}

// HostPort starts (or reuses) the shared container and returns its host and
// mapped port — useful for callers that build their own config (e.g. the e2e
// harness booting InitDependencies from a config.Config).
func HostPort(t *testing.T) (string, int) {
	t.Helper()
	StartPostgres(t)
	return sharedHost, sharedPort
}

func startContainer() (*pgxpool.Pool, string, error) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	req := testcontainers.ContainerRequest{
		Image:        image(),
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     testDBUser,
			"POSTGRES_PASSWORD": testDBPass,
			"POSTGRES_DB":       testDBName,
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(3 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("start container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("container host: %w", err)
	}
	mapped, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, "", fmt.Errorf("mapped port: %w", err)
	}

	sharedHost = host
	sharedPort, err = strconv.Atoi(mapped.Port())
	if err != nil {
		return nil, "", fmt.Errorf("parse mapped port %q: %w", mapped.Port(), err)
	}

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, mapped.Port(), testDBUser, testDBPass, testDBName,
	)

	database, err := db.New(db.Config{
		DSN:             dsn,
		MaxConns:        10,
		MinConns:        2,
		MaxConnLifetime: 5 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}, logger)
	if err != nil {
		return nil, "", fmt.Errorf("connect pool: %w", err)
	}

	if err := database.RunMigrations(); err != nil {
		return nil, "", fmt.Errorf("run migrations: %w", err)
	}

	return database.Pool, dsn, nil
}

// Truncate empties the named tables (RESTART IDENTITY CASCADE) so each test
// starts from a clean slate. Mirrors the per-domain clearXTable helpers.
func Truncate(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	for _, table := range tables {
		_, err := pool.Exec(context.Background(),
			fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", table))
		if err != nil {
			t.Fatalf("testsupport: truncate %s: %v", table, err)
		}
	}
}
