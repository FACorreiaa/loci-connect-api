package compare

import (
	"context"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/google/uuid"
)

// Candidate limits per plan. Free gets a straight either/or comparison; Pro gets
// a route through as many cities as the proto allows, which is what the
// multi-city planner was built for.
const (
	FreeMaxCandidates = 2
	ProMaxCandidates  = 8
)

// Entitlements describes what a user's plan lets them do in compare.
type Entitlements struct {
	// MaxCandidates caps how many cities can be compared and routed at once.
	MaxCandidates int
	// AllowMultiCity gates the planned route (the "do all of these" outline and
	// its export). Free users still see the comparison, just not the itinerary.
	AllowMultiCity bool
}

// EntitlementsForUser returns the compare limits for a user's plan.
func EntitlementsForUser(ctx context.Context, plans PlanChecker, userID uuid.UUID) Entitlements {
	free := Entitlements{MaxCandidates: FreeMaxCandidates, AllowMultiCity: false}

	if plans == nil || userID == uuid.Nil {
		return free
	}
	plan, err := plans.EffectivePlan(ctx, userID)
	if err != nil {
		return free
	}
	if subscription.IsProPlan(plan) {
		return Entitlements{MaxCandidates: ProMaxCandidates, AllowMultiCity: true}
	}
	return free
}
