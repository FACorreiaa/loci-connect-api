package travelhistory

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	travelhistoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory/travelhistoryconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements TravelHistoryService.
type Handler struct {
	travelhistoryconnect.UnimplementedTravelHistoryServiceHandler
	repo Repository
	log  *slog.Logger
}

// NewHandler builds the travel-history Connect handler.
func NewHandler(repo Repository, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{repo: repo, log: log.With(slog.String("component", "travelhistory-handler"))}
}

// userID and toConnectErr are local rather than shared: the domains in this
// codebase each carry their own copy (see trip/handler.go), and inventing a
// shared helper package for two functions would be a wider change than this
// work calls for.
func userID(ctx context.Context) (uuid.UUID, error) {
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return id, nil
}

func toConnectErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrInvalidInput):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// pageBounds converts a page/pageSize pair into limit/offset, clamping to the
// same ceiling the proto validates against.
func pageBounds(page, pageSize int32) (limit, offset int) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}
	if page < 1 {
		page = 1
	}
	return int(pageSize), int(page-1) * int(pageSize)
}

// ensureBackfilled runs the lazy derivation and reports whether history is
// populated. A backfill failure is logged and swallowed: a user with no derived
// history should still see their live history rather than an error page.
func (h *Handler) ensureBackfilled(ctx context.Context, uid uuid.UUID) bool {
	done, err := h.repo.EnsureBackfilled(ctx, uid)
	if err != nil {
		h.log.Warn("travel history backfill failed",
			slog.String("user_id", uid.String()),
			slog.String("error", err.Error()))
		return false
	}
	return done
}

func (h *Handler) ListVisitedCities(
	ctx context.Context,
	req *connect.Request[travelhistoryv1.ListVisitedCitiesRequest],
) (*connect.Response[travelhistoryv1.ListVisitedCitiesResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	h.ensureBackfilled(ctx, uid)

	limit, offset := pageBounds(req.Msg.GetPage(), req.Msg.GetPageSize())
	cities, total, err := h.repo.ListVisitedCities(ctx, uid, limit, offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&travelhistoryv1.ListVisitedCitiesResponse{
		Cities: visitedCitiesToProto(cities),
		Total:  int32(total),
	}), nil
}

func (h *Handler) ListVisitedPOIs(
	ctx context.Context,
	req *connect.Request[travelhistoryv1.ListVisitedPOIsRequest],
) (*connect.Response[travelhistoryv1.ListVisitedPOIsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	h.ensureBackfilled(ctx, uid)

	cityID, err := parseOptionalUUID(req.Msg.GetCityId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid city ID"))
	}
	limit, offset := pageBounds(req.Msg.GetPage(), req.Msg.GetPageSize())
	pois, total, err := h.repo.ListVisitedPOIs(ctx, uid, cityID, limit, offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&travelhistoryv1.ListVisitedPOIsResponse{
		Pois:  visitedPOIsToProto(pois),
		Total: int32(total),
	}), nil
}

func (h *Handler) GetTravelSummary(
	ctx context.Context,
	req *connect.Request[travelhistoryv1.GetTravelSummaryRequest],
) (*connect.Response[travelhistoryv1.GetTravelSummaryResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	h.ensureBackfilled(ctx, uid)

	summary, err := h.repo.Summary(ctx, uid, req.Msg.GetPeriodDays())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&travelhistoryv1.GetTravelSummaryResponse{
		Summary: summaryToProto(summary),
	}), nil
}

func (h *Handler) RecordVisit(
	ctx context.Context,
	req *connect.Request[travelhistoryv1.RecordVisitRequest],
) (*connect.Response[travelhistoryv1.RecordVisitResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	in, err := visitInputFromProto(req.Msg)
	if err != nil {
		return nil, toConnectErr(err)
	}
	city, err := h.repo.RecordVisit(ctx, uid, in)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&travelhistoryv1.RecordVisitResponse{
		City: visitedCityToProto(city),
	}), nil
}

func (h *Handler) DeleteVisit(
	ctx context.Context,
	req *connect.Request[travelhistoryv1.DeleteVisitRequest],
) (*connect.Response[travelhistoryv1.DeleteVisitResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid visit ID"))
	}
	if err := h.repo.DeleteVisit(ctx, uid, id); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&travelhistoryv1.DeleteVisitResponse{Deleted: true}), nil
}

// GetGlobeData fills the whole globe dashboard in one call. Three separate RPCs
// would make one screen wait on three sequential auth + query cycles.
func (h *Handler) GetGlobeData(
	ctx context.Context,
	req *connect.Request[travelhistoryv1.GetGlobeDataRequest],
) (*connect.Response[travelhistoryv1.GetGlobeDataResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	backfilled := h.ensureBackfilled(ctx, uid)

	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = DefaultGlobeLimit
	}
	cities, arcs, err := h.repo.GlobeData(ctx, uid, limit)
	if err != nil {
		return nil, toConnectErr(err)
	}
	summary, err := h.repo.Summary(ctx, uid, req.Msg.GetPeriodDays())
	if err != nil {
		return nil, toConnectErr(err)
	}

	return connect.NewResponse(&travelhistoryv1.GetGlobeDataResponse{
		Cities:     visitedCitiesToProto(cities),
		Arcs:       arcsToProto(arcs),
		Summary:    summaryToProto(summary),
		Backfilled: backfilled,
	}), nil
}
