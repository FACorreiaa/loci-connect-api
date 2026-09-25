package entitlement

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	entitlementv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/entitlement/v1"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type stubPlans struct{ plan string }

func (s stubPlans) EffectivePlan(context.Context, uuid.UUID) (string, error) {
	return s.plan, nil
}

type stubLists struct {
	lists  int
	places int
}

func (s stubLists) CountUserLists(context.Context, uuid.UUID) (int, error) {
	return s.lists, nil
}

func (s stubLists) CountUserListItems(context.Context, uuid.UUID) (int, error) {
	return s.places, nil
}

type stubFavs struct{ n int }

func (s stubFavs) GetFavoritesCount(context.Context, uuid.UUID, string) (int, error) {
	return s.n, nil
}

func TestGetEntitlements_FreeUnifiedSaves(t *testing.T) {
	withGating(t, true)
	uid := uuid.New()
	h := NewHandler(stubPlans{plan: subscription.PlanFree}, stubLists{lists: 2, places: 10}, stubFavs{n: 5})
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, uid.String())

	res, err := h.GetEntitlements(ctx, connect.NewRequest(&entitlementv1.GetEntitlementsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	e := res.Msg
	if e.Plan != subscription.PlanFree || e.ListsUsed != 2 || e.ListsLimit != 5 {
		t.Fatalf("lists/plan: %+v", e)
	}
	if e.PlacesSaved != 15 || e.PlacesLimit != 50 {
		t.Fatalf("want places 15/50, got %d/%d", e.PlacesSaved, e.PlacesLimit)
	}
	if e.AdvancedFilters || e.ExportFull {
		t.Fatal("free should not unlock pro flags")
	}
}

func TestGetEntitlements_ProUnlimited(t *testing.T) {
	withGating(t, true)
	uid := uuid.New()
	h := NewHandler(stubPlans{plan: subscription.PlanPremiumMonthly}, stubLists{lists: 99, places: 500}, stubFavs{n: 100})
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, uid.String())

	res, err := h.GetEntitlements(ctx, connect.NewRequest(&entitlementv1.GetEntitlementsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	e := res.Msg
	if e.ListsLimit != -1 || e.PlacesLimit != -1 {
		t.Fatalf("pro limits should be -1: %+v", e)
	}
	if e.PlacesSaved != 600 {
		t.Fatalf("want 600 saves, got %d", e.PlacesSaved)
	}
	if !e.AdvancedFilters || !e.ExportFull {
		t.Fatal("pro flags should be true")
	}
}

// With gating off (the default) a free account reads exactly like Pro, so the
// clients open every export and stop counting lists and places.
func TestGetEntitlements_UngatedFreeIsUnlimited(t *testing.T) {
	withGating(t, false)
	uid := uuid.New()
	h := NewHandler(stubPlans{plan: subscription.PlanFree}, stubLists{lists: 2, places: 10}, stubFavs{n: 5})
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, uid.String())

	res, err := h.GetEntitlements(ctx, connect.NewRequest(&entitlementv1.GetEntitlementsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	e := res.Msg
	if e.Plan != subscription.PlanFree {
		t.Fatalf("the plan itself stays truthful: %+v", e)
	}
	if e.ListsLimit != -1 || e.PlacesLimit != -1 || !e.AdvancedFilters || !e.ExportFull {
		t.Fatalf("ungated free should be unlimited with every flag on: %+v", e)
	}
}

func withGating(t *testing.T, on bool) {
	t.Helper()
	previous := subscription.Gating()
	subscription.SetGating(on)
	t.Cleanup(func() { subscription.SetGating(previous) })
}
