package travelhistory

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	travelhistoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory/travelhistoryconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements TravelHistoryService.
type Handler struct {
	travelhistoryconnect.UnimplementedTravelHistoryServiceHandler
	repo   Repository
	log    *slog.Logger
	scorer VisitScorer
	taste  preference.Recorder
	kicker preference.Kicker
	// rankings, when set, loses a place's ranking with its last visit.
	rankings RankingRemover
}

// RankingRemover deletes a traveller's ranking of one place (ranking.Service).
type RankingRemover interface {
	Delete(ctx context.Context, userID uuid.UUID, poiID string) (bool, error)
}

// VisitScorer awards points for a visit made on the spot. It returns the
// points and never fails the visit (gamification.Service via an adapter).
type VisitScorer interface {
	ScoreOnTheSpot(ctx context.Context, userID uuid.UUID, v ScoredVisit) int
}

// ScoredVisit is what the scorer needs from a recorded visit.
type ScoredVisit struct {
	POIID string
	// NewCity is set when this visit put the city on the user's globe.
	NewCity *VisitedCity
	Device  *DeviceFix
}

// DeviceFix is where the device was when it recorded the visit.
type DeviceFix struct {
	Latitude, Longitude float64
	AccuracyM           float64
	ObservedAt          time.Time
}

// WithScorer turns on points for visits recorded on the spot.
func (h *Handler) WithScorer(s VisitScorer) *Handler {
	h.scorer = s
	return h
}

// WithTaste makes recorded place visits teach the traveller's taste profile.
// Derived visits (trip backfill, recommendation events) do not pass through
// this handler, so they are never counted twice.
func (h *Handler) WithTaste(r preference.Recorder, k preference.Kicker) *Handler {
	h.taste = r
	h.kicker = k
	return h
}

// WithRankings makes deleting the last visit to a place also delete its
// ranking: a place the traveller says they never went to should not sit in
// the top list their friends see.
func (h *Handler) WithRankings(r RankingRemover) *Handler {
	h.rankings = r
	return h
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
	city, visitID, err := h.repo.RecordVisit(ctx, uid, in)
	if err != nil {
		return nil, toConnectErr(err)
	}
	h.learnFromVisit(ctx, uid, in, visitID)
	var points int
	if h.scorer != nil && req.Msg.DeviceLocation != nil {
		fix := req.Msg.GetDeviceLocation()
		v := ScoredVisit{
			POIID: in.POIID,
			Device: &DeviceFix{
				Latitude:   fix.GetLatitude(),
				Longitude:  fix.GetLongitude(),
				AccuracyM:  fix.GetAccuracyM(),
				ObservedAt: fix.GetObservedAt().AsTime(),
			},
		}
		if city != nil && city.VisitCount == 1 {
			v.NewCity = city
		}
		points = h.scorer.ScoreOnTheSpot(ctx, uid, v)
	}
	return connect.NewResponse(&travelhistoryv1.RecordVisitResponse{
		City:          visitedCityToProto(city),
		PointsAwarded: int32(points),
	}), nil
}

// visitKey is the preference_feedback metadata key a visit's signal is filed under.
const visitKey = "visit_id"

// learnFromVisit records a newly inserted place visit as a taste signal. The
// visit id rides in the metadata so deleting the visit can remove the signal.
func (h *Handler) learnFromVisit(ctx context.Context, uid uuid.UUID, in VisitInput, visitID *uuid.UUID) {
	if h.taste == nil || visitID == nil || in.POIID == "" {
		return
	}
	h.taste.Record(ctx, uid, preference.EventVisited, preference.RecordOpts{
		POIID:  in.POIID,
		TripID: in.TripID,
		Weight: 1,
		Metadata: map[string]any{
			visitKey: visitID.String(),
			"source": string(in.Source),
		},
	})
	if h.kicker != nil {
		h.kicker.Kick(uid)
	}
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

// DeleteVisitedPOI removes one place visit and the taste signal it produced.
func (h *Handler) DeleteVisitedPOI(
	ctx context.Context,
	req *connect.Request[travelhistoryv1.DeleteVisitedPOIRequest],
) (*connect.Response[travelhistoryv1.DeleteVisitedPOIResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid visit ID"))
	}
	gone, err := h.repo.DeleteVisitedPOI(ctx, uid, id)
	if err != nil {
		return nil, toConnectErr(err)
	}
	if gone.Deleted && h.taste != nil {
		h.taste.Forget(ctx, uid, visitKey, id.String())
		if h.kicker != nil {
			h.kicker.Kick(uid)
		}
	}
	if gone.LastOfPOI && h.rankings != nil {
		// Best-effort, like the taste signal: the visit is already gone.
		if _, err := h.rankings.Delete(ctx, uid, gone.POIID); err != nil {
			h.log.WarnContext(ctx, "delete ranking with last visit", slog.String("poi_id", gone.POIID), slog.Any("error", err))
		}
	}
	return connect.NewResponse(&travelhistoryv1.DeleteVisitedPOIResponse{Deleted: gone.Deleted}), nil
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
