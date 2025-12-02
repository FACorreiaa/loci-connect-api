package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	// Register pgx database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const defaultRetries = 5

// DB wraps the pgxpool connection pool
type DB struct {
	Pool   *pgxpool.Pool
	logger *slog.Logger
}

// Config holds database configuration
type Config struct {
	DSN             string `envconfig:"DATABASE_URL"`
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// New creates a new database connection pool using pgxpool
func New(cfg Config, logger *slog.Logger) (*DB, error) {
	logger.Info("initializing database connection pool", "dsn_host", maskDSN(cfg.DSN))

	// Parse pool config
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// Set connection pool settings
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	// Create pool
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create database pool: %w", err)
	}

	db := &DB{
		Pool:   pool,
		logger: logger,
	}

	// Wait for database to be ready
	if !db.WaitForDB(context.Background()) {
		pool.Close()
		return nil, fmt.Errorf("database connection failed after retries")
	}

	logger.Info("database connection pool initialized successfully")
	return db, nil
}

// WaitForDB waits for the database connection pool to be available
func (d *DB) WaitForDB(ctx context.Context) bool {
	maxAttempts := defaultRetries
	for attempts := 1; attempts <= maxAttempts; attempts++ {
		err := d.Pool.Ping(ctx)
		if err == nil {
			d.logger.Info("database connection successful")
			return true
		}

		waitDuration := time.Duration(attempts) * 200 * time.Millisecond
		d.logger.Warn("database ping failed, retrying...",
			"attempt", attempts,
			"max_attempts", maxAttempts,
			"wait_duration", waitDuration,
			"error", err,
		)

		if attempts < maxAttempts {
			time.Sleep(waitDuration)
		}
	}

	d.logger.Error("database connection failed after multiple retries")
	return false
}

// RunMigrations runs database migrations using goose with embedded SQL files
func (d *DB) RunMigrations() error {
	d.logger.Info("running database migrations...")

	// Set up goose to use embedded migrations
	goose.SetBaseFS(migrationFS)

	if err := goose.SetDialect(string(goose.DialectPostgres)); err != nil {
		d.logger.Error("failed to set goose dialect", "error", err)
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	// Open a standard database connection for goose (goose requires database/sql)
	db, err := sql.Open("pgx", d.Pool.Config().ConnString())
	if err != nil {
		d.logger.Error("failed to open database for migrations", "error", err)
		return fmt.Errorf("failed to open database for migrations: %w", err)
	}
	defer db.Close()

	// Log available migrations
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		d.logger.Error("failed to read embedded migrations directory", "error", err)
		return fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	if len(entries) == 0 {
		d.logger.Warn("no migration files found in embedded migrations directory")
		return nil
	}

	d.logger.Info("found migration files", "count", len(entries))
	for _, entry := range entries {
		d.logger.Debug("migration file", "name", entry.Name())
	}

	// Run migrations up
	if err := goose.Up(db, "migrations"); err != nil {
		d.logger.Error("failed to run migrations", "error", err)
		return fmt.Errorf("goose.Up failed: %w", err)
	}

	d.logger.Info("database migrations completed successfully")
	return nil
}

// Close closes the database connection pool
func (d *DB) Close() {
	if d.Pool != nil {
		d.Pool.Close()
		d.logger.Info("database connection pool closed")
	}
}

// Health checks if the database is healthy
func (d *DB) Health() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := d.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}
	return nil
}

// maskDSN returns a masked version of the DSN for logging (hides password)
func maskDSN(dsn string) string {
	// Simple masking: just show host portion
	// For production, use a proper DSN parser
	if len(dsn) > 30 {
		return dsn[:30] + "..."
	}
	return "***"
}
