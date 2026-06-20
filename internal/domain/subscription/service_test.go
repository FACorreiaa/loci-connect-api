package subscription

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeUsageRepo is an in-memory Repository for testing the rate-limit service
// without a database.
type fakeUsageRepo struct {
	usage     int
	usageErr  error
	plan      string
	planErr   error
	getCalls  int
	planCalls int
	incCalls  int
	incErr    error
	lastIncID uuid.UUID
}

func (f *fakeUsageRepo) GetDailyUsage(_ context.Context, _ uuid.UUID) (int, error) {
	f.getCalls++
	return f.usage, f.usageErr
}

func (f *fakeUsageRepo) GetUserPlan(_ context.Context, _ uuid.UUID) (string, error) {
	f.planCalls++
	if f.plan == "" {
		return "free", f.planErr
	}
	return f.plan, f.planErr
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

func TestCheckRateLimit_PremiumPlanHighLimit(t *testing.T) {
	repo := &fakeUsageRepo{usage: 100, plan: "premium_monthly"}
	svc := NewService(repo, testLogger(), "admin@example.com")

	if err := svc.CheckRateLimit(context.Background(), uuid.New(), "user@example.com"); err != nil {
		t.Fatalf("premium usage under unlimited cap should pass, got %v", err)
	}
}

func TestCheckRateLimit_PaidPlanTenPerDay(t *testing.T) {
	repo := &fakeUsageRepo{usage: 10, plan: "paid"}
	svc := NewService(repo, testLogger(), "admin@example.com")

	err := svc.CheckRateLimit(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("paid plan at 10 requests should be quota exceeded, got %v", err)
	}
}

func TestDailyLimitForPlan(t *testing.T) {
	if got := dailyLimitForPlan("free"); got != 5 {
		t.Fatalf("free limit = %d, want 5", got)
	}
	if got := dailyLimitForPlan("paid"); got != 10 {
		t.Fatalf("paid limit = %d, want 10", got)
	}
	if got := dailyLimitForPlan("premium_monthly"); got != unlimitedDailyRequests {
		t.Fatalf("premium limit = %d, want unlimited", got)
	}
}

func TestEffectivePlan(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	if got := effectivePlan("premium_monthly", "active", nil); got != "premium_monthly" {
		t.Fatalf("active plan = %q", got)
	}
	if got := effectivePlan("premium_monthly", "canceled", &future); got != "premium_monthly" {
		t.Fatalf("canceled with future end = %q", got)
	}
	if got := effectivePlan("premium_monthly", "canceled", &past); got != string(TierFree) {
		t.Fatalf("canceled with past end = %q, want free", got)
	}
	if got := effectivePlan("premium_monthly", "expired", nil); got != string(TierFree) {
		t.Fatalf("expired plan = %q, want free", got)
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
