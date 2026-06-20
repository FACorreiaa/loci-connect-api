package subscription

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
)

var (
	ErrQuotaExceeded = errors.New("daily request quota exceeded")
)

type Tier string

const (
	TierFree    Tier = "free"
	TierPaid    Tier = "paid"
	TierPremium Tier = "premium"
	TierAdmin   Tier = "admin"
)

type Service interface {
	CheckRateLimit(ctx context.Context, userID uuid.UUID, email string) error
	RecordUsage(ctx context.Context, userID uuid.UUID) error
	// GetUserTier would ideally come from DB or Claims.
	// For MVP I might rely on Claims passed in context or DB lookup if needed.
}

type service struct {
	repo       Repository
	logger     *slog.Logger
	adminEmail string
}

func NewService(repo Repository, logger *slog.Logger, adminEmail string) Service {
	return &service{
		repo:       repo,
		logger:     logger,
		adminEmail: adminEmail,
	}
}

func (s *service) CheckRateLimit(ctx context.Context, userID uuid.UUID, email string) error {
	// 1. Admin Bypass
	if email == s.adminEmail {
		return nil
	}

	// 2. Determine Tier & Limit
	// TODO: Fetch real tier from DB `subscriptions` table.
	// For now, defaulting to Free tier as per MVP, unless we read it from context claims in Interceptor.
	// But let's assume we can fetch it or pass it.
	// For MVP efficiently: We can assume Free unless proven otherwise.
	// Let's check DB usage first as that's always needed.

	plan, err := s.repo.GetUserPlan(ctx, userID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to get subscription plan", "error", err)
		return err
	}

	usage, err := s.repo.GetDailyUsage(ctx, userID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to get usage", "error", err)
		return err
	}

	limit := dailyLimitForPlan(plan)
	if usage >= limit {
		s.logger.InfoContext(ctx, "daily quota exceeded",
			"user_id", userID,
			"plan", plan,
			"usage", usage,
			"limit", limit,
		)
		return ErrQuotaExceeded
	}

	return nil
}

func (s *service) RecordUsage(ctx context.Context, userID uuid.UUID) error {
	return s.repo.IncrementUsage(ctx, userID)
}
