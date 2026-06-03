package subscription

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

// fakeUsageRepo is an in-memory Repository for testing the rate-limit service
// without a database.
type fakeUsageRepo struct {
	usage     int
	usageErr  error
	getCalls  int
	incCalls  int
	incErr    error
	lastIncID uuid.UUID
}

func (f *fakeUsageRepo) GetDailyUsage(_ context.Context, _ uuid.UUID) (int, error) {
	f.getCalls++
	return f.usage, f.usageErr
}

func (f *fakeUsageRepo) IncrementUsage(_ context.Context, id uuid.UUID) error {
	f.incCalls++
	f.lastIncID = id
	return f.incErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCheckRateLimit_AdminBypass(t *testing.T) {
	repo := &fakeUsageRepo{usage: 999} // would exceed if checked
	svc := NewService(repo, testLogger(), "admin@example.com")

	if err := svc.CheckRateLimit(context.Background(), uuid.New(), "admin@example.com"); err != nil {
		t.Fatalf("admin should bypass rate limit, got %v", err)
	}
	if repo.getCalls != 0 {
		t.Fatalf("admin bypass must not query usage, got %d calls", repo.getCalls)
	}
}

func TestCheckRateLimit_UnderLimit(t *testing.T) {
	repo := &fakeUsageRepo{usage: 4}
	svc := NewService(repo, testLogger(), "admin@example.com")

	if err := svc.CheckRateLimit(context.Background(), uuid.New(), "user@example.com"); err != nil {
		t.Fatalf("usage under limit should pass, got %v", err)
	}
}

func TestCheckRateLimit_AtLimit(t *testing.T) {
	repo := &fakeUsageRepo{usage: 5} // free tier limit is 5
	svc := NewService(repo, testLogger(), "admin@example.com")

	err := svc.CheckRateLimit(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("usage at limit should be quota exceeded, got %v", err)
	}
}

func TestCheckRateLimit_OverLimit(t *testing.T) {
	repo := &fakeUsageRepo{usage: 50}
	svc := NewService(repo, testLogger(), "admin@example.com")

	err := svc.CheckRateLimit(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("usage over limit should be quota exceeded, got %v", err)
	}
}

func TestCheckRateLimit_RepoErrorFailsSafe(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &fakeUsageRepo{usageErr: sentinel}
	svc := NewService(repo, testLogger(), "admin@example.com")

	err := svc.CheckRateLimit(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, sentinel) {
		t.Fatalf("repo error should propagate (fail safe), got %v", err)
	}
}

func TestRecordUsage(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewService(repo, testLogger(), "admin@example.com")
	id := uuid.New()

	if err := svc.RecordUsage(context.Background(), id); err != nil {
		t.Fatalf("RecordUsage returned error: %v", err)
	}
	if repo.incCalls != 1 {
		t.Fatalf("expected 1 increment, got %d", repo.incCalls)
	}
	if repo.lastIncID != id {
		t.Fatalf("increment used wrong id: got %s want %s", repo.lastIncID, id)
	}
}

func TestRecordUsage_PropagatesError(t *testing.T) {
	sentinel := errors.New("increment failed")
	repo := &fakeUsageRepo{incErr: sentinel}
	svc := NewService(repo, testLogger(), "admin@example.com")

	if err := svc.RecordUsage(context.Background(), uuid.New()); !errors.Is(err, sentinel) {
		t.Fatalf("RecordUsage should propagate repo error, got %v", err)
	}
}
