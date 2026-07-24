package compare

import (
	"context"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/google/uuid"
)

// EntitlementsForUser returns freemium flags for compare.
func EntitlementsForUser(ctx context.Context, plans PlanChecker, userID uuid.UUID) (allow3, allowDual bool) {
	if plans == nil || userID == uuid.Nil {
		return false, false
	}
	plan, err := plans.EffectivePlan(ctx, userID)
	if err != nil {
		return false, false
	}
	if subscription.IsProPlan(plan) {
		return true, true
	}
	return false, false
}
