package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The classifier decides whether the job waits or gives up. Getting it wrong in
// one direction loses the night's work; in the other it retries a real bug
// until the budget expires, which is worse because it hides it.
func TestDatabaseUnavailable(t *testing.T) {
	retryable := []struct {
		name string
		err  error
	}{
		{
			// The exact error every failed run logged between 09-15 and 09-18.
			name: "in recovery mode",
			err:  &pgconn.PgError{Code: "57P03", Message: "the database system is in recovery mode"},
		},
		{
			name: "not yet accepting connections",
			err:  &pgconn.PgError{Code: "57P03", Message: "the database system is not yet accepting connections"},
		},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}},
		{"crash shutdown", &pgconn.PgError{Code: "57P02"}},
		{
			// What the first write saw at 03:15:02, a second after the ping
			// had succeeded.
			name: "connection cut mid-query",
			err:  fmt.Errorf("failed to update POI embedding: %w", io.ErrUnexpectedEOF),
		},
		{
			name: "nothing listening yet",
			err:  errors.New("failed to connect: dial error: dial tcp 10.43.132.104:5432: connect: connection refused"),
		},
		{"reset by peer", errors.New("read tcp: connection reset by peer")},
		{"wrapped pg error", fmt.Errorf("acquire reranker lock connection: %w", &pgconn.PgError{Code: "57P03"})},
	}

	for _, tc := range retryable {
		t.Run(tc.name, func(t *testing.T) {
			if !databaseUnavailable(tc.err) {
				t.Errorf("databaseUnavailable(%v) = false, want true — the job would give up and lose the night", tc.err)
			}
		})
	}

	// Anything that waiting cannot fix has to surface, not spin.
	notRetryable := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"syntax error", &pgconn.PgError{Code: "42601", Message: "syntax error at or near"}},
		{"undefined column", &pgconn.PgError{Code: "42703"}},
		{"unique violation", &pgconn.PgError{Code: "23505"}},
		{"permission denied", &pgconn.PgError{Code: "42501"}},
		{"an ordinary bug", errors.New("nil pointer dereference in reranker")},
	}

	for _, tc := range notRetryable {
		t.Run(tc.name, func(t *testing.T) {
			if databaseUnavailable(tc.err) {
				t.Errorf("databaseUnavailable(%v) = true, want false — this would retry a real bug until the budget expired", tc.err)
			}
		})
	}
}

// A run that fails for a non-database reason must not be repeated.
func TestRunWithDatabaseRetryDoesNotRetryRealErrors(t *testing.T) {
	calls := 0
	boom := errors.New("reranker panicked")

	err := runWithDatabaseRetry(context.Background(), func() error {
		calls++
		return boom
	}, nil, time.Minute, slog.New(slog.DiscardHandler))

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the original error returned untouched", err)
	}
	if calls != 1 {
		t.Errorf("run called %d times, want 1", calls)
	}
}

// The happy path must not wait on anything.
func TestRunWithDatabaseRetrySucceedsFirstTime(t *testing.T) {
	calls := 0

	err := runWithDatabaseRetry(context.Background(), func() error {
		calls++
		return nil
	}, nil, time.Minute, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("run called %d times, want 1", calls)
	}
}

// An exhausted budget returns the database error rather than hanging, so the
// pod exits and the failure is visible.
func TestRunWithDatabaseRetryGivesUpWhenTheBudgetIsGone(t *testing.T) {
	calls := 0
	down := &pgconn.PgError{Code: "57P03", Message: "the database system is in recovery mode"}

	err := runWithDatabaseRetry(context.Background(), func() error {
		calls++
		return down
	}, nil, -time.Second, slog.New(slog.DiscardHandler))

	if !errors.Is(err, down) {
		t.Errorf("err = %v, want the database error", err)
	}
	if calls != 1 {
		t.Errorf("run called %d times, want 1 — an expired budget should not start another attempt", calls)
	}
}
