package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

// This job runs at 03:15, and something on the host takes Postgres down around
// then: every run between 2026-09-15 and 2026-09-18 failed on all three
// attempts, logging `the database system is in recovery mode (SQLSTATE 57P03)`
// and recording items_updated = 0. Run by hand at midday against a healthy
// database, the same code processes the backlog in about a minute.
//
// So the work was never the problem — the job simply could not survive the
// database being unavailable for the minute or two it takes to come back. The
// pool's own WaitForDB gives up after five attempts and roughly two seconds,
// which is the right budget for an API that must fail fast and the wrong one
// for a nightly batch job with hours of slack.
//
// Waiting longer alone would not have been enough. At 03:15:01 the ping
// succeeded and the very next write died with an unexpected EOF: the database
// was flapping, not merely slow. So the retry has to wrap the work, not just
// the connect.

// databaseUnavailable reports whether an error means "the database is not
// there right now" rather than "this operation is wrong".
//
// Retrying the first kind is how the job survives a restart. Retrying the
// second would just loop on a real bug, so everything else is returned as-is.
func databaseUnavailable(err error) bool {
	if err == nil {
		return false
	}

	// Postgres is up and answering, but refusing connections while it starts
	// or recovers. 57P03 covers both "not yet accepting connections" and
	// "in recovery mode"; both are temporary by definition.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "57P03", // cannot_connect_now
			"57P01", // admin_shutdown
			"57P02": // crash_shutdown
			return true
		}
	}

	// The connection was cut mid-query — what a backend being killed looks
	// like from this side.
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}

	// Nothing is listening yet. pgx wraps the dial error rather than exposing
	// a typed one, so the text is all there is to match on.
	msg := err.Error()
	for _, fragment := range []string{
		"connection refused",
		"unexpected EOF",
		"connection reset by peer",
		"broken pipe",
		"no route to host",
		"server closed the connection unexpectedly",
		"the database system is",
	} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}

	return false
}

// waitForDatabase blocks until the database will actually serve a query, or the
// budget runs out.
//
// It runs a real query rather than pinging. A ping can succeed against a
// database that is still recovering and will reject the next statement, which
// is exactly the trap that made this job fail while appearing to connect.
func waitForDatabase(ctx context.Context, pool *pgxpool.Pool, budget time.Duration, logger *slog.Logger) error {
	deadline := time.Now().Add(budget)
	wait := time.Second
	const maxWait = 30 * time.Second

	for attempt := 1; ; attempt++ {
		var inRecovery bool
		err := pool.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&inRecovery)
		switch {
		case err == nil && !inRecovery:
			if attempt > 1 {
				logger.Info("database is ready", "attempts", attempt)
			}
			return nil
		case err == nil && inRecovery:
			// A replica would legitimately sit here, but this job only ever
			// talks to the primary, so this means crash recovery.
			err = errors.New("database is in recovery")
		case !databaseUnavailable(err):
			// Something other than availability is wrong; waiting will not fix
			// it and would hide it.
			return err
		}

		if time.Now().After(deadline) {
			return errors.New("database did not become ready within " + budget.String() + ": " + err.Error())
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		logger.Warn("waiting for the database to come back",
			"attempt", attempt,
			"retry_in", wait,
			"budget_left", time.Until(deadline).Round(time.Second),
			"error", err,
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}

		if wait < maxWait {
			wait *= 2
			if wait > maxWait {
				wait = maxWait
			}
		}
	}
}

// connectWithin keeps trying to open the pool until the budget runs out.
//
// db.New already retries, but only for about two seconds — enough for a pod
// starting a moment ahead of its database, not for a database that is away for
// a minute.
func connectWithin(ctx context.Context, cfg db.Config, budget time.Duration, logger *slog.Logger) (*db.DB, error) {
	deadline := time.Now().Add(budget)
	wait := time.Second
	const maxWait = 30 * time.Second

	for attempt := 1; ; attempt++ {
		database, err := db.New(cfg, logger)
		if err == nil {
			return database, nil
		}
		if !databaseUnavailable(err) || time.Now().After(deadline) {
			return nil, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		logger.Warn("database not up yet, waiting",
			"attempt", attempt,
			"retry_in", wait,
			"budget_left", time.Until(deadline).Round(time.Second),
			"error", err,
		)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if wait < maxWait {
			wait *= 2
			if wait > maxWait {
				wait = maxWait
			}
		}
	}
}

// runWithDatabaseRetry runs the job, and runs it again if the database went
// away underneath it.
//
// Every stage is an idempotent backfill — it asks what is missing and fills it
// — so a repeat after a failed attempt costs a little duplicated reading and
// nothing else. That is what makes retrying the whole run safe rather than
// having to thread resumption through each stage.
func runWithDatabaseRetry(
	ctx context.Context,
	run func() error,
	pool *pgxpool.Pool,
	budget time.Duration,
	logger *slog.Logger,
) error {
	deadline := time.Now().Add(budget)

	for attempt := 1; ; attempt++ {
		err := run()
		if err == nil {
			return nil
		}
		if !databaseUnavailable(err) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}

		logger.Warn("the database went away mid-run, waiting for it to come back",
			"attempt", attempt,
			"budget_left", time.Until(deadline).Round(time.Second),
			"error", err,
		)

		// Wait for it to genuinely serve a query again before trying once more,
		// rather than charging straight back in and failing the same way.
		if waitErr := waitForDatabase(ctx, pool, time.Until(deadline), logger); waitErr != nil {
			return waitErr
		}
	}
}
