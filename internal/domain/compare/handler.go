package compare

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	comparev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/compare/v1/comparev1connect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements CompareService.
type Handler struct {
	comparev1connect.UnimplementedCompareServiceHandler
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CompareWeekend(
	ctx context.Context,
	req *connect.Request[comparev1.CompareWeekendRequest],
) (*connect.Response[comparev1.CompareWeekendResponse], error) {
	if h.svc == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("compare service unavailable"))
	}

	candidates := req.Msg.GetCandidateCityNames()
	if len(candidates) < 2 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("provide 2–3 candidate cities"))
	}

	var uid uuid.UUID
	if s, ok := interceptors.GetUserIDFromContext(ctx); ok && s != "" {
		if parsed, err := uuid.Parse(s); err == nil {
			uid = parsed
		}
	}

	ent := EntitlementsForUser(ctx, h.svc.plans, uid)
	if len(candidates) > ent.MaxCandidates {
		candidates = candidates[:ent.MaxCandidates]
	}

	start := req.Msg.GetStartDate().AsTime()
	end := req.Msg.GetEndDate().AsTime()

	out, err := h.svc.CompareWeekend(ctx, CompareInput{
		OriginCity:       req.Msg.GetOriginCity(),
		OriginLat:        req.Msg.GetOriginLat(),
		OriginLon:        req.Msg.GetOriginLon(),
		Candidates:       candidates,
		Start:            start,
		End:              end,
		UserID:           uid,
		Allow3Candidates: ent.MaxCandidates > FreeMaxCandidates,
		AllowDualCity:    ent.AllowMultiCity,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("compare: %w", err))
	}
	return connect.NewResponse(out), nil
}
