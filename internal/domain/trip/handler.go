package trip

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/trip"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/trip/tripconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/pkg/apierr"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements TripService.
type Handler struct {
	tripconnect.UnimplementedTripServiceHandler
	repo    Repository
	baseURL string
	prefs   preference.Recorder
	plans   PlanChecker
}

// PlanChecker resolves effective subscription plan for export gating.
type PlanChecker interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

func NewHandler(repo Repository, baseURL string, prefs preference.Recorder, plans PlanChecker) *Handler {
	if prefs == nil {
		prefs = preference.NewRecorder(nil, nil)
	}
	return &Handler{repo: repo, baseURL: baseURL, prefs: prefs, plans: plans}
}

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
	case errors.Is(err, ErrVersionConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (h *Handler) SaveTrip(ctx context.Context, req *connect.Request[tripv1.SaveTripRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	t, err := tripFromProto(req.Msg.GetTrip(), uid)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	saved, err := h.repo.SaveTrip(ctx, t, req.Msg.GetBaseVersion())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(tripToProto(saved)), nil
}

func (h *Handler) GetTrip(ctx context.Context, req *connect.Request[tripv1.GetTripRequest]) (*connect.Response[tripv1.TripDraft], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	t, err := h.repo.GetTrip(ctx, id, uid)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(tripToProto(t)), nil
}

func (h *Handler) ListTrips(ctx context.Context, req *connect.Request[tripv1.ListTripsRequest]) (*connect.Response[tripv1.ListTripsResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	page := int(req.Msg.GetPagination().GetPage())
	limit := int(req.Msg.GetPagination().GetPageSize())
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	trips, total, err := h.repo.ListTrips(ctx, uid, limit, (page-1)*limit)
	if err != nil {
		return nil, toConnectErr(err)
	}
	out := make([]*tripv1.TripDraft, 0, len(trips))
	for _, t := range trips {
		out = append(out, tripToProto(t))
	}
	return connect.NewResponse(&tripv1.ListTripsResponse{
		Trips:      out,
		Pagination: paginationMeta(page, limit, total),
	}), nil
}

func (h *Handler) ShareTrip(ctx context.Context, req *connect.Request[tripv1.ShareTripRequest]) (*connect.Response[tripv1.ShareTripResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	code := newShareCode()
	t, err := h.repo.SetShare(ctx, id, uid, req.Msg.GetIsPublic(), code)
	if err != nil {
		return nil, toConnectErr(err)
	}
	shareCode := code
	if t.ShareCode != nil {
		shareCode = *t.ShareCode
	}
	return connect.NewResponse(&tripv1.ShareTripResponse{
		ShareId:  shareCode,
		ShareUrl: fmt.Sprintf("%s/trip/shared/%s", h.baseURL, shareCode),
	}), nil
}

// mutate loads a trip, applies fn, and saves it with optimistic concurrency.
func (h *Handler) mutate(ctx context.Context, tripID string, baseVersion int64, fn func(*Trip) error) (*connect.Response[tripv1.TripDraft], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(tripID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	t, err := h.repo.GetTrip(ctx, id, uid)
	if err != nil {
		return nil, toConnectErr(err)
	}
	if t.Version != baseVersion {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrVersionConflict)
	}
	if err := fn(t); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	saved, err := h.repo.SaveTrip(ctx, t, baseVersion)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(tripToProto(saved)), nil
}

func (h *Handler) ReorderStops(ctx context.Context, req *connect.Request[tripv1.ReorderStopsRequest]) (*connect.Response[tripv1.TripDraft], error) {
	dayID, err := uuid.Parse(req.Msg.GetDayId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid day ID"))
	}
	resp, err := h.mutate(ctx, req.Msg.GetTripId(), req.Msg.GetBaseVersion(), func(t *Trip) error {
		for di := range t.Days {
			day := &t.Days[di]
			if day.ID != dayID {
				continue
			}
			pos := make(map[string]int, len(req.Msg.GetOrderedStopIds()))
			for i, sid := range req.Msg.GetOrderedStopIds() {
				pos[sid] = i
			}
			for si := range day.Stops {
				if p, ok := pos[day.Stops[si].ID.String()]; ok {
					day.Stops[si].OrderIndex = int32(p)
				}
			}
			sortStopsByOrder(day.Stops)
			return nil
		}
		return errors.New("day not found in trip")
	})
	if err == nil && !tripProtoHasRecommendationTrace(resp.Msg) {
		if uid, uerr := userID(ctx); uerr == nil {
			tid := uuid.MustParse(req.Msg.GetTripId())
			h.prefs.Record(ctx, uid, preference.EventReordered, preference.RecordOpts{
				TripID:   &tid,
				Metadata: map[string]any{"day_id": req.Msg.GetDayId()},
			})
		}
	}
	return resp, err
}

func (h *Handler) RenameStop(ctx context.Context, req *connect.Request[tripv1.RenameStopRequest]) (*connect.Response[tripv1.TripDraft], error) {
	return h.mutate(ctx, req.Msg.GetTripId(), req.Msg.GetBaseVersion(), func(t *Trip) error {
		if s := findStop(t, req.Msg.GetStopId()); s != nil {
			s.Name = req.Msg.GetName()
			return nil
		}
		return errors.New("stop not found")
	})
}

func (h *Handler) EditStopDuration(ctx context.Context, req *connect.Request[tripv1.EditStopDurationRequest]) (*connect.Response[tripv1.TripDraft], error) {
	return h.mutate(ctx, req.Msg.GetTripId(), req.Msg.GetBaseVersion(), func(t *Trip) error {
		s := findStop(t, req.Msg.GetStopId())
		if s == nil {
			return errors.New("stop not found")
		}
		if req.Msg.StartMinute != nil {
			v := req.Msg.GetStartMinute()
			s.StartMinute = &v
		}
		d := req.Msg.GetDurationMinutes()
		s.DurationMinutes = &d
		return nil
	})
}

func (h *Handler) SetConstraint(ctx context.Context, req *connect.Request[tripv1.SetConstraintRequest]) (*connect.Response[tripv1.TripDraft], error) {
	return h.mutate(ctx, req.Msg.GetTripId(), req.Msg.GetBaseVersion(), func(t *Trip) error {
		t.Constraints = constraintFromProto(req.Msg.GetConstraints())
		return nil
	})
}

func (h *Handler) AddStop(ctx context.Context, req *connect.Request[tripv1.AddStopRequest]) (*connect.Response[tripv1.TripDraft], error) {
	dayID, err := uuid.Parse(req.Msg.GetDayId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid day ID"))
	}
	resp, err := h.mutate(ctx, req.Msg.GetTripId(), req.Msg.GetBaseVersion(), func(t *Trip) error {
		for index := range t.Days {
			if t.Days[index].ID != dayID {
				continue
			}
			stop := stopFromProto(req.Msg.GetStop())
			stop.OrderIndex = int32(len(t.Days[index].Stops))
			t.Days[index].Stops = append(t.Days[index].Stops, stop)
			return nil
		}
		return errors.New("day not found in trip")
	})
	if err == nil {
		h.recordStopSignal(ctx, req.Msg.GetTripId(), req.Msg.GetStop(), preference.EventSaved, "added_to_trip")
	}
	return resp, err
}

func (h *Handler) RemoveStop(ctx context.Context, req *connect.Request[tripv1.RemoveStopRequest]) (*connect.Response[tripv1.TripDraft], error) {
	var removed *tripv1.TripStop
	resp, err := h.mutate(ctx, req.Msg.GetTripId(), req.Msg.GetBaseVersion(), func(t *Trip) error {
		for dayIndex := range t.Days {
			for stopIndex := range t.Days[dayIndex].Stops {
				stop := t.Days[dayIndex].Stops[stopIndex]
				if stop.ID.String() != req.Msg.GetStopId() {
					continue
				}
				removed = &tripv1.TripStop{PoiId: stop.POIID, RecommendationTrace: traceToProto(stop.RecommendationTrace)}
				t.Days[dayIndex].Stops = append(t.Days[dayIndex].Stops[:stopIndex], t.Days[dayIndex].Stops[stopIndex+1:]...)
				for index := range t.Days[dayIndex].Stops {
					t.Days[dayIndex].Stops[index].OrderIndex = int32(index)
				}
				return nil
			}
		}
		return errors.New("stop not found")
	})
	if err == nil && removed != nil {
		h.recordStopSignal(ctx, req.Msg.GetTripId(), removed, preference.EventSkipped, "removed_from_trip")
	}
	return resp, err
}

func (h *Handler) ReplaceStop(ctx context.Context, req *connect.Request[tripv1.ReplaceStopRequest]) (*connect.Response[tripv1.TripDraft], error) {
	resp, err := h.mutate(ctx, req.Msg.GetTripId(), req.Msg.GetBaseVersion(), func(t *Trip) error {
		for dayIndex := range t.Days {
			for stopIndex := range t.Days[dayIndex].Stops {
				current := t.Days[dayIndex].Stops[stopIndex]
				if current.ID.String() != req.Msg.GetStopId() {
					continue
				}
				replacement := stopFromProto(req.Msg.GetReplacement())
				replacement.OrderIndex = current.OrderIndex
				t.Days[dayIndex].Stops[stopIndex] = replacement
				return nil
			}
		}
		return errors.New("stop not found")
	})
	if err == nil {
		h.recordStopSignal(ctx, req.Msg.GetTripId(), req.Msg.GetReplacement(), preference.EventSaved, "replaced_trip_stop")
	}
	return resp, err
}

func (h *Handler) recordStopSignal(ctx context.Context, tripID string, stop *tripv1.TripStop, event, action string) {
	uid, err := userID(ctx)
	if err != nil || stop == nil {
		return
	}
	if stop.GetRecommendationTrace() != nil {
		return
	}
	id, err := uuid.Parse(tripID)
	if err != nil {
		return
	}
	metadata := map[string]any{"action": action}
	h.prefs.Record(ctx, uid, event, preference.RecordOpts{POIID: stop.GetPoiId(), TripID: &id, Metadata: metadata})
}

func tripProtoHasRecommendationTrace(trip *tripv1.TripDraft) bool {
	if trip == nil {
		return false
	}
	for _, day := range trip.GetDays() {
		for _, stop := range day.GetStops() {
			if stop.GetRecommendationTrace() != nil {
				return true
			}
		}
	}
	return false
}

func tripHasRecommendationTrace(trip *Trip) bool {
	if trip == nil {
		return false
	}
	for _, day := range trip.Days {
		for _, stop := range day.Stops {
			if stop.RecommendationTrace != nil {
				return true
			}
		}
	}
	return false
}

func (h *Handler) ExportTrip(ctx context.Context, req *connect.Request[tripv1.ExportTripRequest]) (*connect.Response[tripv1.ExportTripResponse], error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetTripId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trip ID"))
	}
	t, err := h.repo.GetTrip(ctx, id, uid)
	if err != nil {
		return nil, toConnectErr(err)
	}
	switch req.Msg.GetFormat() {
	case tripv1.ExportFormat_EXPORT_FORMAT_ICS:
		return connect.NewResponse(&tripv1.ExportTripResponse{
			Data:        []byte(buildICS(t)),
			ContentType: "text/calendar",
			Filename:    safeFilename(t.Title) + ".ics",
		}), nil
	case tripv1.ExportFormat_EXPORT_FORMAT_PDF:
		exportTrip := t
		if h.plans != nil {
			plan, perr := h.plans.EffectivePlan(ctx, uid)
			if perr == nil && !subscription.IsProPlan(plan) && len(t.Days) > 1 {
				// Free: Day 1 only (matches TripKit client soft-gate).
				clone := *t
				clone.Days = append([]TripDay(nil), t.Days[0])
				exportTrip = &clone
			}
		}
		pdfData, err := buildTripPDF(exportTrip)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("pdf: %w", err))
		}
		if !tripHasRecommendationTrace(t) {
			h.prefs.Record(ctx, uid, preference.EventExported, preference.RecordOpts{
				TripID:   &id,
				Metadata: map[string]any{"format": "pdf", "days": len(exportTrip.Days)},
			})
		}
		return connect.NewResponse(&tripv1.ExportTripResponse{
			Data:        pdfData,
			ContentType: "application/pdf",
			Filename:    safeFilename(t.Title) + ".pdf",
		}), nil
	case tripv1.ExportFormat_EXPORT_FORMAT_MARKDOWN:
		if h.plans != nil {
			plan, perr := h.plans.EffectivePlan(ctx, uid)
			if perr == nil && !subscription.IsProPlan(plan) {
				return nil, apierr.ToConnect(&subscription.EntitlementExceededError{
					Feature: "export",
					Limit:   0,
					Used:    0,
				})
			}
		}
		md := buildTripMarkdown(t)
		if !tripHasRecommendationTrace(t) {
			h.prefs.Record(ctx, uid, preference.EventExported, preference.RecordOpts{
				TripID:   &id,
				Metadata: map[string]any{"format": "markdown", "days": len(t.Days)},
			})
		}
		return connect.NewResponse(&tripv1.ExportTripResponse{
			Data:        []byte(md),
			ContentType: "text/markdown; charset=utf-8",
			Filename:    safeFilename(t.Title) + ".md",
		}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported export format"))
	}
}

func findStop(t *Trip, stopID string) *TripStop {
	for di := range t.Days {
		for si := range t.Days[di].Stops {
			if t.Days[di].Stops[si].ID.String() == stopID {
				return &t.Days[di].Stops[si]
			}
		}
	}
	return nil
}

func sortStopsByOrder(stops []TripStop) {
	for i := 1; i < len(stops); i++ {
		for j := i; j > 0 && stops[j-1].OrderIndex > stops[j].OrderIndex; j-- {
			stops[j-1], stops[j] = stops[j], stops[j-1]
		}
	}
}

func safeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "trip"
	}
	repl := strings.NewReplacer("/", "-", "\\", "-", " ", "_", ":", "-")
	return repl.Replace(s)
}
