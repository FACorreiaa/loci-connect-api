package subscription

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"

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

	usage, err := s.repo.GetDailyUsage(ctx, userID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to get usage", "error", err)
		return err // Fail safe or fail open? Fail safe for now.
	}

	// Hardcoded limits for MVP until we wire up full subscription reading
	limit := 5 // Free tier default

	// If we had tier info, we'd switch here.
	// For now, let's enforce 5.
	// Real implementation needs to read subscription.

	if usage >= limit {
		return ErrQuotaExceeded
	}

	return nil
}

func (s *service) RecordUsage(ctx context.Context, userID uuid.UUID) error {
	return s.repo.IncrementUsage(ctx, userID)
}

func getPlatformFeePercent() float64 {
	defaultFee := 30.0
	feeStr := os.Getenv("PLATFORM_FEE_PERCENT")
	if feeStr == "" {
		return defaultFee
	}

	fee, err := strconv.ParseFloat(feeStr, 64)
	if err != nil {
		fmt.Printf("[WARN] Invalid PLATFORM_FEE_PERCENT value: %s, using default %.1f%%", feeStr, defaultFee)
		return defaultFee
	}

	if fee < 0 || fee > 100 {
		fmt.Printf("[WARN] PLATFORM_FEE_PERCENT out of range (0-100): %.1f, using default %.1f%%", fee, defaultFee)
		return defaultFee
	}

	return fee
}
