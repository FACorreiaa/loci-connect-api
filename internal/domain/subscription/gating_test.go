package subscription

import "testing"

// Plan gating is off until the product has users to gate (see docs/pricing.md
// and the iOS roadmap's "Deferred — Pro gating"): every plan is entitled to
// every feature, while what a plan *is* (IsProPlan) stays truthful for billing.
func TestEntitled_OffByDefault(t *testing.T) {
	if Gating() {
		t.Fatal("gating should be off by default")
	}
	if !Entitled(PlanFree) || !Entitled("") || !Entitled(PlanPremiumAnnual) {
		t.Fatal("with gating off every plan is entitled")
	}
	if IsProPlan(PlanFree) {
		t.Fatal("IsProPlan must keep reporting the real plan")
	}
	if ListLimitForPlan(PlanFree) != -1 || PlaceLimitForPlan(PlanFree) != -1 {
		t.Fatalf("ungated free limits should be unlimited, got %d/%d", ListLimitForPlan(PlanFree), PlaceLimitForPlan(PlanFree))
	}
	if err := CheckListCreate(PlanFree, 500); err != nil {
		t.Fatalf("ungated free should never hit the list cap: %v", err)
	}
}

func TestEntitled_GatedFollowsThePlan(t *testing.T) {
	withGating(t, true)
	if Entitled(PlanFree) || !Entitled(PlanPremiumMonthly) {
		t.Fatal("with gating on only Pro is entitled")
	}
	if ListLimitForPlan(PlanFree) != FreeMaxLists || PlaceLimitForPlan(PlanFree) != FreeMaxPlaces {
		t.Fatal("gated free limits should be the free caps")
	}
}
