package gamification

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	gamificationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gamification"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gamification/gamificationconnect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Handler implements GamificationService.
type Handler struct {
	gamificationconnect.UnimplementedGamificationServiceHandler
	svc *Service
	log *slog.Logger
}

// NewHandler wires the handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

func caller(ctx context.Context) (uuid.UUID, error) {
	s, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || s == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}
	return id, nil
}

func (h *Handler) connectErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		h.log.Error("gamification rpc failed", slog.Any("error", err))
		return connect.NewError(connect.CodeInternal, errors.New("something went wrong, try again"))
	}
}

func badgeProto(b Badge, at time.Time, earned bool) *gamificationv1.Badge {
	out := &gamificationv1.Badge{Id: b.ID, Title: b.Title, Description: b.Description}
	if earned {
		out.AwardedAt = timestamppb.New(at)
	}
	return out
}

func badgesProto(bs []Badge, now time.Time) []*gamificationv1.Badge {
	out := make([]*gamificationv1.Badge, 0, len(bs))
	for _, b := range bs {
		out = append(out, badgeProto(b, now, true))
	}
	return out
}

func progressProto(p *Progress) *gamificationv1.Progress {
	out := &gamificationv1.Progress{
		TotalPoints:       p.Totals.TotalPoints,
		Level:             int32(p.Level),
		PointsToNextLevel: p.ToNext,
		CurrentStreak:     int32(p.Totals.CurrentStreak),
		LongestStreak:     int32(p.Totals.LongestStreak),
		Today: &gamificationv1.TodayChecklist{
			CheckedIn:     p.CheckedIn,
			Searched:      p.Searched,
			PlacesVisited: int32(p.PlacesToday),
		},
	}
	for _, b := range Badges {
		at, earned := p.Earned[b.ID]
		out.Badges = append(out.Badges, badgeProto(b, at, earned))
	}
	return out
}

func (h *Handler) GetMyProgress(ctx context.Context, _ *connect.Request[gamificationv1.GetMyProgressRequest]) (*connect.Response[gamificationv1.GetMyProgressResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.MyProgress(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&gamificationv1.GetMyProgressResponse{Progress: progressProto(p)}), nil
}

func (h *Handler) DailyCheckIn(ctx context.Context, req *connect.Request[gamificationv1.DailyCheckInRequest]) (*connect.Response[gamificationv1.DailyCheckInResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := h.svc.CheckIn(ctx, uid, req.Msg.GetTimezone())
	if err != nil {
		return nil, h.connectErr(err)
	}
	p, err := h.svc.MyProgress(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&gamificationv1.DailyCheckInResponse{
		Progress:      progressProto(p),
		PointsAwarded: int32(res.Points),
		NewBadges:     badgesProto(res.NewBadges, h.svc.now()),
	}), nil
}

func periodKind(p gamificationv1.LeaderboardPeriod) PeriodKind {
	switch p {
	case gamificationv1.LeaderboardPeriod_LEADERBOARD_PERIOD_MONTH:
		return PeriodMonth
	case gamificationv1.LeaderboardPeriod_LEADERBOARD_PERIOD_ALL_TIME:
		return PeriodAllTime
	default:
		return PeriodWeek
	}
}

func metric(m gamificationv1.LeaderboardMetric) Metric {
	switch m {
	case gamificationv1.LeaderboardMetric_LEADERBOARD_METRIC_CITIES:
		return MetricCities
	case gamificationv1.LeaderboardMetric_LEADERBOARD_METRIC_PLACES:
		return MetricPlaces
	default:
		return MetricPoints
	}
}

func (h *Handler) GetLeaderboard(ctx context.Context, req *connect.Request[gamificationv1.GetLeaderboardRequest]) (*connect.Response[gamificationv1.GetLeaderboardResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	entries, period, err := h.svc.Leaderboard(ctx, uid, periodKind(req.Msg.GetPeriod()), metric(req.Msg.GetMetric()))
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := &gamificationv1.GetLeaderboardResponse{}
	for _, e := range entries {
		level, _ := LevelFor(e.Totals.TotalPoints)
		out.Entries = append(out.Entries, &gamificationv1.LeaderboardEntry{
			User:          e.User,
			Rank:          int32(e.Rank),
			Value:         e.Value,
			Level:         int32(level),
			CurrentStreak: int32(e.Totals.CurrentStreak),
			IsMe:          e.IsMe,
		})
	}
	if !period.From.IsZero() {
		out.PeriodStart = timestamppb.New(period.From)
		out.PeriodEnd = timestamppb.New(period.To.AddDate(0, 0, 1))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) ListPointsHistory(ctx context.Context, req *connect.Request[gamificationv1.ListPointsHistoryRequest]) (*connect.Response[gamificationv1.ListPointsHistoryResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	var before time.Time
	if tok := req.Msg.GetPageToken(); tok != "" {
		if before, err = time.Parse(time.RFC3339Nano, tok); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page token"))
		}
	}
	size := int(req.Msg.GetPageSize())
	events, err := h.svc.History(ctx, uid, size, before)
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := &gamificationv1.ListPointsHistoryResponse{}
	for _, e := range events {
		out.Events = append(out.Events, &gamificationv1.PointsEvent{
			Id:        e.ID.String(),
			Kind:      gamificationv1.PointsKind(e.Kind),
			Points:    int32(e.Points),
			Label:     e.Label,
			CreatedAt: timestamppb.New(e.CreatedAt),
		})
	}
	if size <= 0 || size > 100 {
		size = 30
	}
	if len(events) == size {
		out.NextPageToken = events[len(events)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) CompleteTripDay(ctx context.Context, req *connect.Request[gamificationv1.CompleteTripDayRequest]) (*connect.Response[gamificationv1.CompleteTripDayResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := h.svc.CompleteTripDay(ctx, uid, req.Msg.GetTripId(), req.Msg.GetDayId(), int(req.Msg.GetStopsDone()), req.Msg.GetTimezone())
	if err != nil {
		return nil, h.connectErr(err)
	}
	p, err := h.svc.MyProgress(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&gamificationv1.CompleteTripDayResponse{
		Progress:      progressProto(p),
		PointsAwarded: int32(res.Points),
		TripCompleted: res.TripCompleted,
		NewBadges:     badgesProto(res.NewBadges, h.svc.now()),
	}), nil
}
