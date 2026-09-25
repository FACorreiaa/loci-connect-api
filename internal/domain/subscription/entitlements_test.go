package subscription

import (
	"errors"
	"testing"
)

func TestCheckListCreate(t *testing.T) {
	withGating(t, true)
	if err := CheckListCreate(PlanFree, 4); err != nil {
		t.Fatalf("expected allow at 4/5, got %v", err)
	}
	if err := CheckListCreate(PlanFree, 5); err == nil {
		t.Fatal("expected deny at 5/5")
	}
	if err := CheckListCreate(PlanPremiumMonthly, 100); err != nil {
		t.Fatalf("pro should be unlimited, got %v", err)
	}
}

func TestCheckPlaceAdd(t *testing.T) {
	withGating(t, true)
	if err := CheckPlaceAdd(PlanFree, 49); err != nil {
		t.Fatalf("expected allow at 49/50, got %v", err)
	}
	err := CheckPlaceAdd(PlanFree, 50)
	if err == nil {
		t.Fatal("expected deny at 50/50")
	}
	var ee *EntitlementExceededError
	if !errors.As(err, &ee) || ee.Feature != "places" || ee.Limit != FreeMaxPlaces {
		t.Fatalf("want places entitlement error, got %v", err)
	}
	if !errors.Is(err, ErrEntitlementExceeded) {
		t.Fatal("expected errors.Is ErrEntitlementExceeded")
	}
}

// withGating flips plan gating for one test and restores it after. Tests that
// touch it cannot run in parallel with each other.
func withGating(t *testing.T, on bool) {
	t.Helper()
	previous := Gating()
	SetGating(on)
	t.Cleanup(func() { SetGating(previous) })
}
