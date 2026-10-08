package gamification

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	gamificationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gamification"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/gamification/gamificationconnect"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
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
	events, err := h.svc.History(ctx, uid, size, before, req.Msg.GetFieldOnly())
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := &gamificationv1.ListPointsHistoryResponse{}
	for _, e := range events {
		pe := &gamificationv1.PointsEvent{
			Id:          e.ID.String(),
			Kind:        gamificationv1.PointsKind(e.Kind),
			Points:      int32(e.Points),
			Label:       e.Label,
			CreatedAt:   timestamppb.New(e.CreatedAt),
			CityName:    e.CityName,
			FieldPoints: int32(e.FieldPoints),
			SeasonId:    int32(e.SeasonID),
		}
		if e.CityID != nil {
			pe.CityId = e.CityID.String()
		}
		out.Events = append(out.Events, pe)
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

func (h *Handler) GetFieldProfile(ctx context.Context, _ *connect.Request[gamificationv1.GetFieldProfileRequest]) (*connect.Response[gamificationv1.GetFieldProfileResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.Profile(ctx, uid)
	if err != nil {
		return nil, h.connectErr(err)
	}
	out := &gamificationv1.FieldProfile{
		LifetimeScore:        p.Lifetime,
		OverallRank:          gamificationv1.FieldRank(p.Rank),
		OverallNextThreshold: p.Next,
		WeekScore:            p.Week,
		LastWeekScore:        p.LastWeek,
		SeasonId:             int32(p.SeasonID),
		PlacesKept:           int32(p.PlacesKept),
		DaysFinished:         int32(p.DaysFinished),
	}
	for _, c := range p.Cities {
		out.Cities = append(out.Cities, &gamificationv1.CityRank{
			CityId:        c.CityID.String(),
			CityName:      c.Name,
			Rank:          gamificationv1.FieldRank(c.Rank),
			Score:         c.Score,
			NextThreshold: c.Next,
		})
	}
	return connect.NewResponse(&gamificationv1.GetFieldProfileResponse{Profile: out}), nil
}

func fieldRowProto(r *FieldRow) *gamificationv1.FieldBoardRow {
	if r == nil {
		return nil
	}
	return &gamificationv1.FieldBoardRow{
		Position:    int32(r.Position),
		DisplayName: r.DisplayName,
		User:        r.User,
		Value:       r.Value,
		IsMe:        r.IsMe,
		Rank:        gamificationv1.FieldRank(r.Rank),
	}
}

func (h *Handler) GetFieldBoard(ctx context.Context, req *connect.Request[gamificationv1.GetFieldBoardRequest]) (*connect.Response[gamificationv1.GetFieldBoardResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.svc.Board(ctx, uid, BoardRequest{
		Scope:        Scope(req.Msg.GetScope()),
		CityID:       req.Msg.GetCityId(),
		Metric:       FieldMetric(req.Msg.GetMetric()),
		SeasonOffset: int(req.Msg.GetSeasonOffset()),
	})
	if err != nil {
		return nil, h.connectErr(err)
	}
	start := SeasonStart(b.SeasonID)
	out := &gamificationv1.GetFieldBoardResponse{
		Scope:            gamificationv1.FieldBoardScope(b.Scope),
		CityName:         b.CityName,
		SeasonId:         int32(b.SeasonID),
		SeasonStart:      timestamppb.New(start),
		SeasonEnd:        timestamppb.New(start.AddDate(0, 0, 7)),
		Me:               fieldRowProto(b.Me),
		Above:            fieldRowProto(b.Above),
		ScoredUsers:      int32(b.Scored),
		TooFew:           b.TooFew,
		FriendsAvailable: b.FriendsAvailable,
		HistoryLocked:    b.HistoryLocked,
		MeHidden:         b.MeHidden,
	}
	if b.CityID != nil {
		out.CityId = b.CityID.String()
	}
	for i := range b.Top {
		out.Top = append(out.Top, fieldRowProto(&b.Top[i]))
	}
	if p := b.Personal; p != nil {
		out.Personal = &gamificationv1.PersonalWeek{
			ThisWeek:             p.This.Score,
			LastWeek:             p.Last.Score,
			PlacesKept:           int32(p.This.PlacesKept),
			PlacesKeptLastWeek:   int32(p.Last.PlacesKept),
			DaysFinished:         int32(p.This.DaysFinished),
			DaysFinishedLastWeek: int32(p.Last.DaysFinished),
		}
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) MarkStop(ctx context.Context, req *connect.Request[gamificationv1.MarkStopRequest]) (*connect.Response[gamificationv1.MarkStopResponse], error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := h.svc.MarkStop(ctx, uid, req.Msg.GetTripId(), req.Msg.GetStopId(),
		StopStatus(req.Msg.GetStatus()), req.Msg.GetTimezone())
	if err != nil {
		return nil, h.connectErr(err)
	}
	return connect.NewResponse(&gamificationv1.MarkStopResponse{
		Status:        tripv1.TripStopStatus(res.Status),
		PointsAwarded: int32(res.Points),
		DayFinished:   res.DayFinished,
		TripFinished:  res.TripFinished,
		WeekScore:     res.WeekScore,
	}), nil
}
