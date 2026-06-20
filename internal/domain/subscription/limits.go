package subscription

import "time"

const unlimitedDailyRequests = 1_000_000

// dailyLimitForPlan maps subscription plan names to daily request quotas.
// See docs/pricing.md for product intent.
func dailyLimitForPlan(plan string) int {
	switch plan {
	case "premium_monthly", "premium_annual", "premium", "pro":
		return unlimitedDailyRequests
	case "paid", "explorer":
		return 10
	default:
		return 5
	}
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
		return string(TierFree)
	default:
		return string(TierFree)
	}
}