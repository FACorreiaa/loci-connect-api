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

// fakeUsageRepo is an in-memory Repository for testing the quota service
// without a database.
type fakeUsageRepo struct {
	usage     int
	usageErr  error
	plan      string
	planErr   error
	incErr    error
	getCalls  int
	planCalls int
	incCalls  int
	lastLimit int
	lastIncID uuid.UUID
}

func (f *fakeUsageRepo) GetDailyUsage(_ context.Context, _ uuid.UUID) (int, error) {
	f.getCalls++
	return f.usage, f.usageErr
}

func (f *fakeUsageRepo) GetUserPlan(_ context.Context, _ uuid.UUID) (string, error) {
	f.planCalls++
	if f.plan == "" {
		return PlanFree, f.planErr
	}
	return f.plan, f.planErr
}

func (f *fakeUsageRepo) TryIncrementUsage(_ context.Context, id uuid.UUID, limit int) (bool, int, error) {
	f.incCalls++
	f.lastIncID = id
	f.lastLimit = limit
	if f.incErr != nil {
		return false, 0, f.incErr
	}
	if f.usage >= limit {
		return false, 0, nil
	}
	f.usage++
	return true, f.usage, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestService(repo Repository, adminEmail string) Service {
	return NewService(repo, testLogger(), adminEmail, DefaultLimits())
}

func TestConsumeQuota_AdminBypass(t *testing.T) {
	repo := &fakeUsageRepo{usage: 999} // would exceed if checked
	svc := newTestService(repo, "admin@example.com")

	if err := svc.ConsumeQuota(context.Background(), uuid.New(), "admin@example.com"); err != nil {
		t.Fatalf("admin should bypass quota, got %v", err)
	}
	if repo.planCalls != 0 || repo.incCalls != 0 {
		t.Fatalf("admin bypass must not touch repo, got plan=%d inc=%d", repo.planCalls, repo.incCalls)
	}
}

func TestConsumeQuota_EmptyAdminEmailNeverBypasses(t *testing.T) {
	// A missing ADMIN_EMAIL must not turn empty-email claims into admins.
	repo := &fakeUsageRepo{usage: 0}
	svc := newTestService(repo, "")

	if err := svc.ConsumeQuota(context.Background(), uuid.New(), ""); err != nil {
		t.Fatalf("expected normal consumption, got %v", err)
	}
	if repo.incCalls != 1 {
		t.Fatalf("expected repo consumption for empty admin email, inc=%d", repo.incCalls)
	}
}

func TestConsumeQuota_UnderLimit(t *testing.T) {
	repo := &fakeUsageRepo{usage: 9} // free limit is 10
	svc := newTestService(repo, "admin@example.com")
	id := uuid.New()

	if err := svc.ConsumeQuota(context.Background(), id, "user@example.com"); err != nil {
		t.Fatalf("usage under limit should pass, got %v", err)
	}
	if repo.lastIncID != id {
		t.Fatalf("consumed wrong user: got %s want %s", repo.lastIncID, id)
	}
	if repo.lastLimit != 10 {
		t.Fatalf("free plan limit = %d, want 10", repo.lastLimit)
	}
}

func TestConsumeQuota_AtLimit(t *testing.T) {
	repo := &fakeUsageRepo{usage: 10}
	svc := newTestService(repo, "admin@example.com")

	err := svc.ConsumeQuota(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("usage at limit should be quota exceeded, got %v", err)
	}
	var quotaErr *QuotaExceededError
	if !errors.As(err, &quotaErr) {
		t.Fatalf("expected *QuotaExceededError, got %T", err)
	}
	if quotaErr.Plan != PlanFree || quotaErr.Limit != 10 {
		t.Fatalf("quota error = %+v, want plan=free limit=10", quotaErr)
	}
}

func TestConsumeQuota_ProPlanFairUseCap(t *testing.T) {
	// Derived from the configured cap so tuning PRO_DAILY_LLM_LIMIT does not
	// silently invalidate the boundary this test is asserting.
	cap := DefaultLimits().ProDaily
	repo := &fakeUsageRepo{usage: cap - 1, plan: PlanPremiumMonthly}
	svc := newTestService(repo, "admin@example.com")

	if err := svc.ConsumeQuota(context.Background(), uuid.New(), "user@example.com"); err != nil {
		t.Fatalf("pro one under the cap should pass, got %v", err)
	}

	// Now at the cap: fair use reached.
	err := svc.ConsumeQuota(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("pro at the cap should be denied, got %v", err)
	}
	var quotaErr *QuotaExceededError
	if !errors.As(err, &quotaErr) || !IsProPlan(quotaErr.Plan) {
		t.Fatalf("expected pro-plan quota error, got %v", err)
	}
}

func TestConsumeQuota_UnknownPlanFallsBackToFreeLimit(t *testing.T) {
	// Legacy strings that never existed in the DB enum must get free limits,
	// not silently unlimited.
	for _, plan := range []string{"paid", "explorer", "premium", "pro"} {
		repo := &fakeUsageRepo{plan: plan}
		svc := newTestService(repo, "admin@example.com")

		if err := svc.ConsumeQuota(context.Background(), uuid.New(), "user@example.com"); err != nil {
			t.Fatalf("plan %q: %v", plan, err)
		}
		if repo.lastLimit != 10 {
			t.Fatalf("plan %q limit = %d, want free limit 10", plan, repo.lastLimit)
		}
	}
}

func TestConsumeQuota_ZeroLimitDeniesWithoutIncrement(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewService(repo, testLogger(), "", Limits{FreeDaily: 0, ProDaily: 300})

	err := svc.ConsumeQuota(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("zero limit should deny, got %v", err)
	}
	if repo.incCalls != 0 {
		t.Fatalf("zero limit must not hit the counter, inc=%d", repo.incCalls)
	}
}

func TestConsumeQuota_PlanErrorFailsSafe(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &fakeUsageRepo{planErr: sentinel}
	svc := newTestService(repo, "admin@example.com")

	err := svc.ConsumeQuota(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, sentinel) {
		t.Fatalf("plan lookup error should propagate (fail safe), got %v", err)
	}
}

func TestConsumeQuota_IncrementErrorFailsSafe(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &fakeUsageRepo{incErr: sentinel}
	svc := newTestService(repo, "admin@example.com")

	err := svc.ConsumeQuota(context.Background(), uuid.New(), "user@example.com")
	if !errors.Is(err, sentinel) {
		t.Fatalf("increment error should propagate (fail safe), got %v", err)
	}
}

func TestConsumeQuota_PlanCached(t *testing.T) {
	repo := &fakeUsageRepo{plan: PlanPremiumMonthly}
	svc := newTestService(repo, "admin@example.com")
	id := uuid.New()

	for range 3 {
		if err := svc.ConsumeQuota(context.Background(), id, "user@example.com"); err != nil {
			t.Fatalf("ConsumeQuota: %v", err)
		}
	}
	if repo.planCalls != 1 {
		t.Fatalf("plan should be cached across calls, got %d lookups", repo.planCalls)
	}
}

func TestInvalidatePlan_ForcesFreshLookup(t *testing.T) {
	repo := &fakeUsageRepo{plan: PlanPremiumMonthly}
	svc := newTestService(repo, "admin@example.com")
	id := uuid.New()

	if err := svc.ConsumeQuota(context.Background(), id, "user@example.com"); err != nil {
		t.Fatalf("ConsumeQuota: %v", err)
	}
	svc.InvalidatePlan(id)
	if err := svc.ConsumeQuota(context.Background(), id, "user@example.com"); err != nil {
		t.Fatalf("ConsumeQuota after invalidate: %v", err)
	}
	if repo.planCalls != 2 {
		t.Fatalf("invalidate should force fresh plan lookup, got %d lookups", repo.planCalls)
	}
}

func TestConsumeQuota_PlanCacheExpires(t *testing.T) {
	repo := &fakeUsageRepo{plan: PlanPremiumMonthly}
	svc := newTestService(repo, "admin@example.com").(*service)
	id := uuid.New()

	current := time.Now()
	svc.now = func() time.Time { return current }

	if err := svc.ConsumeQuota(context.Background(), id, "user@example.com"); err != nil {
		t.Fatalf("ConsumeQuota: %v", err)
	}
	current = current.Add(planCacheTTL + time.Second)
	if err := svc.ConsumeQuota(context.Background(), id, "user@example.com"); err != nil {
		t.Fatalf("ConsumeQuota after expiry: %v", err)
	}
	if repo.planCalls != 2 {
		t.Fatalf("expired cache entry should refetch plan, got %d lookups", repo.planCalls)
	}
}

func TestDailyLimitForPlan(t *testing.T) {
	l := DefaultLimits()
	if got := l.dailyLimitForPlan(PlanFree); got != 10 {
		t.Fatalf("free limit = %d, want 10", got)
	}
	if got := l.dailyLimitForPlan(PlanPremiumMonthly); got != 100 {
		t.Fatalf("premium_monthly limit = %d, want 100", got)
	}
	if got := l.dailyLimitForPlan(PlanPremiumAnnual); got != 100 {
		t.Fatalf("premium_annual limit = %d, want 100", got)
	}
	if got := l.dailyLimitForPlan("unknown"); got != 10 {
		t.Fatalf("unknown plan limit = %d, want free limit 10", got)
	}
}

func TestEffectivePlan(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	if got := effectivePlan(PlanPremiumMonthly, "active", nil); got != PlanPremiumMonthly {
		t.Fatalf("active plan = %q", got)
	}
	if got := effectivePlan(PlanPremiumMonthly, "canceled", &future); got != PlanPremiumMonthly {
		t.Fatalf("canceled with future end = %q", got)
	}
	if got := effectivePlan(PlanPremiumMonthly, "canceled", &past); got != PlanFree {
		t.Fatalf("canceled with past end = %q, want free", got)
	}
	if got := effectivePlan(PlanPremiumMonthly, "expired", nil); got != PlanFree {
		t.Fatalf("expired plan = %q, want free", got)
	}
}
