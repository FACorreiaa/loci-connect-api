package subscription

import "time"

// Plan names as stored in the subscriptions.plan enum (subscription_plan_type).
// Both premium values are marketed as the single "Pro" plan.
const (
	PlanFree           = "free"
	PlanPremiumMonthly = "premium_monthly"
	PlanPremiumAnnual  = "premium_annual"
)

// Limits holds per-plan daily LLM request quotas, sourced from config
// (FREE_DAILY_LLM_LIMIT / PRO_DAILY_LLM_LIMIT). The Pro limit is a hidden
// fair-use cap; the product surface advertises Pro as unlimited.
type Limits struct {
	FreeDaily int
	ProDaily  int
}

// DefaultLimits mirrors the config defaults for callers without config access.
func DefaultLimits() Limits {
	return Limits{FreeDaily: 10, ProDaily: 300}
}

// dailyLimitForPlan maps subscription plan names to daily request quotas.
// Unknown values (including legacy strings that never existed in the DB enum)
// deliberately fall back to the free limit. See docs/pricing.md.
func (l Limits) dailyLimitForPlan(plan string) int {
	if isProPlan(plan) {
		return l.ProDaily
	}
	return l.FreeDaily
}

func isProPlan(plan string) bool {
	return plan == PlanPremiumMonthly || plan == PlanPremiumAnnual
}

// effectivePlan returns the plan whose limits should apply given DB status.
func effectivePlan(plan, status string, endDate *time.Time) string {
	switch status {
	case "active", "trialing", "past_due":
		return plan
	case "canceled":
		if endDate != nil && endDate.After(time.Now()) {
			return plan
		}
		return PlanFree
	default:
		return PlanFree
	}
}
