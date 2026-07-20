package subscription

import (
	"errors"
	"fmt"
)

// Free-tier caps from PRICING.md. Pro plans are unlimited (-1).
const (
	FreeMaxLists  = 5
	FreeMaxPlaces = 50
)

// ErrEntitlementExceeded is returned when a free-tier user hits a feature limit
// (lists, saves). Callers should map it to Connect CodePermissionDenied and
// surface an upgrade CTA.
var ErrEntitlementExceeded = errors.New("plan entitlement exceeded")

// EntitlementExceededError carries which limit was hit so clients can show the
// right upgrade copy.
type EntitlementExceededError struct {
	Feature string // "lists" | "places"
	Limit   int
	Used    int
}

func (e *EntitlementExceededError) Error() string {
	return fmt.Sprintf("%s limit reached (%d/%d on free plan)", e.Feature, e.Used, e.Limit)
}

func (e *EntitlementExceededError) Is(target error) bool {
	return target == ErrEntitlementExceeded
}

// ListLimitForPlan returns the max top-level lists for a plan (-1 = unlimited).
func ListLimitForPlan(plan string) int {
	if IsProPlan(plan) {
		return -1
	}
	return FreeMaxLists
}

// PlaceLimitForPlan returns the max saved places (list items + favorites) for a plan.
func PlaceLimitForPlan(plan string) int {
	if IsProPlan(plan) {
		return -1
	}
	return FreeMaxPlaces
}

// CheckListCreate returns ErrEntitlementExceeded when free user is at list cap.
func CheckListCreate(plan string, currentLists int) error {
	limit := ListLimitForPlan(plan)
	if limit < 0 {
		return nil
	}
	if currentLists >= limit {
		return &EntitlementExceededError{Feature: "lists", Limit: limit, Used: currentLists}
	}
	return nil
}

// CheckPlaceAdd returns ErrEntitlementExceeded when free user is at place cap.
func CheckPlaceAdd(plan string, currentPlaces int) error {
	limit := PlaceLimitForPlan(plan)
	if limit < 0 {
		return nil
	}
	if currentPlaces >= limit {
		return &EntitlementExceededError{Feature: "places", Limit: limit, Used: currentPlaces}
	}
	return nil
}
