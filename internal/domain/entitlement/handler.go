package entitlement

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	entitlementv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/entitlement/v1"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/entitlement/v1/entitlementv1connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// PlanChecker resolves the caller's effective subscription plan.
type PlanChecker interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

// ListCounter counts owned lists and list items.
type ListCounter interface {
	CountUserLists(ctx context.Context, userID uuid.UUID) (int, error)
	CountUserListItems(ctx context.Context, userID uuid.UUID) (int, error)
}

// FavoritesCounter counts saved favorites.
type FavoritesCounter interface {
	GetFavoritesCount(ctx context.Context, userID uuid.UUID, contentType string) (int, error)
}

// Handler implements EntitlementService.
type Handler struct {
	entitlementv1connect.UnimplementedEntitlementServiceHandler
	plans     PlanChecker
	lists     ListCounter
	favorites FavoritesCounter
}

func NewHandler(plans PlanChecker, lists ListCounter, favorites FavoritesCounter) *Handler {
	return &Handler{plans: plans, lists: lists, favorites: favorites}
}

func (h *Handler) GetEntitlements(
	ctx context.Context,
	_ *connect.Request[entitlementv1.GetEntitlementsRequest],
) (*connect.Response[entitlementv1.Entitlements], error) {
	uidStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || uidStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid user id"))
	}

	plan := subscription.PlanFree
	if h.plans != nil {
		p, perr := h.plans.EffectivePlan(ctx, uid)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("plan: %w", perr))
		}
		if p != "" {
			plan = p
		}
	}

	listsUsed := 0
	if h.lists != nil {
		n, lerr := h.lists.CountUserLists(ctx, uid)
		if lerr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("lists: %w", lerr))
		}
		listsUsed = n
	}

	placesSaved := 0
	if h.lists != nil {
		n, lerr := h.lists.CountUserListItems(ctx, uid)
		if lerr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list items: %w", lerr))
		}
		placesSaved += n
	}
	if h.favorites != nil {
		n, ferr := h.favorites.GetFavoritesCount(ctx, uid, "")
		if ferr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("favorites: %w", ferr))
		}
		placesSaved += n
	}

	isPro := subscription.IsProPlan(plan)
	out := &entitlementv1.Entitlements{
		Plan:             plan,
		ListsUsed:        int32(listsUsed),
		ListsLimit:       int32(subscription.ListLimitForPlan(plan)),
		PlacesSaved:      int32(placesSaved),
		PlacesLimit:      int32(subscription.PlaceLimitForPlan(plan)),
		AdvancedFilters:  isPro,
		ExportFull:       isPro,
	}
	return connect.NewResponse(out), nil
}
