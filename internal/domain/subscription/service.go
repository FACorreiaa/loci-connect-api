package subscription

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
	"github.com/google/uuid"
)

// ErrQuotaExceeded is the sentinel matched via errors.Is; the concrete error
// returned is *QuotaExceededError, which carries plan and limit details.
var ErrQuotaExceeded = errors.New("daily request quota exceeded")

// QuotaExceededError reports which plan and limit produced a quota denial so
// the interceptor can distinguish a free-tier denial (upgrade CTA) from a Pro
// fair-use denial (no CTA).
type QuotaExceededError struct {
	Plan  string
	Limit int
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("daily request quota exceeded (plan %s, limit %d)", e.Plan, e.Limit)
}

func (e *QuotaExceededError) Is(target error) bool { return target == ErrQuotaExceeded }

const (
	planCacheTTL        = 60 * time.Second
	maxPlanCacheEntries = 10_000
)

// PlanInvalidator lets other domains (Stripe webhook processing) evict a
// user's cached plan immediately after an entitlement change.
type PlanInvalidator interface {
	InvalidatePlan(userID uuid.UUID)
}

type Service interface {
	// ConsumeQuota atomically spends one daily LLM request for the user.
	// Returns *QuotaExceededError when the plan's daily limit is reached.
	ConsumeQuota(ctx context.Context, userID uuid.UUID, email string) error
	PlanInvalidator
}

type planEntry struct {
	plan    string
	expires time.Time
}

type service struct {
	repo       Repository
	logger     *slog.Logger
	adminEmail string
	limits     Limits

	mu        sync.Mutex
	planCache map[uuid.UUID]planEntry
	now       func() time.Time
}

func NewService(repo Repository, logger *slog.Logger, adminEmail string, limits Limits) Service {
	return &service{
		repo:       repo,
		logger:     logger,
		adminEmail: adminEmail,
		limits:     limits,
		planCache:  make(map[uuid.UUID]planEntry),
		now:        time.Now,
	}
}

func (s *service) ConsumeQuota(ctx context.Context, userID uuid.UUID, email string) error {
	if s.adminEmail != "" && email == s.adminEmail {
		return nil
	}

	plan, err := s.userPlan(ctx, userID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to get subscription plan", "error", err, "user_id", userID)
		return err
	}

	limit := s.limits.dailyLimitForPlan(plan)
	if limit <= 0 {
		// A zero/negative configured limit disables the tier entirely; the
		// upsert below cannot express this because a first-of-day insert
		// always succeeds.
		observability.QuotaDenialsTotal.WithLabelValues(plan).Inc()
		return &QuotaExceededError{Plan: plan, Limit: limit}
	}

	allowed, _, err := s.repo.TryIncrementUsage(ctx, userID, limit)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to consume daily quota", "error", err, "user_id", userID)
		return err
	}
	if !allowed {
		s.logger.InfoContext(ctx, "daily quota exceeded",
			"user_id", userID,
			"plan", plan,
			"limit", limit,
		)
		observability.QuotaDenialsTotal.WithLabelValues(plan).Inc()
		return &QuotaExceededError{Plan: plan, Limit: limit}
	}

	observability.QuotaConsumedTotal.WithLabelValues(plan).Inc()
	return nil
}

// userPlan returns the user's effective plan, cached for planCacheTTL to
// avoid a subscriptions lookup on every LLM request. Webhook-driven plan
// changes call InvalidatePlan so upgrades take effect immediately.
func (s *service) userPlan(ctx context.Context, userID uuid.UUID) (string, error) {
	s.mu.Lock()
	entry, ok := s.planCache[userID]
	if ok && s.now().Before(entry.expires) {
		s.mu.Unlock()
		return entry.plan, nil
	}
	s.mu.Unlock()

	plan, err := s.repo.GetUserPlan(ctx, userID)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	if len(s.planCache) >= maxPlanCacheEntries {
		s.evictExpiredLocked()
	}
	s.planCache[userID] = planEntry{plan: plan, expires: s.now().Add(planCacheTTL)}
	s.mu.Unlock()
	return plan, nil
}

// evictExpiredLocked drops stale entries; if everything is still live it
// clears the map rather than growing without bound. Callers hold s.mu.
func (s *service) evictExpiredLocked() {
	now := s.now()
	for id, entry := range s.planCache {
		if !now.Before(entry.expires) {
			delete(s.planCache, id)
		}
	}
	if len(s.planCache) >= maxPlanCacheEntries {
		s.planCache = make(map[uuid.UUID]planEntry)
	}
}

func (s *service) InvalidatePlan(userID uuid.UUID) {
	s.mu.Lock()
	delete(s.planCache, userID)
	s.mu.Unlock()
}
