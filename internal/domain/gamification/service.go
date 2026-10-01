package gamification

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

// Graph is what the leaderboard needs from the friends layer.
// social.Service satisfies it.
type Graph interface {
	FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error)
}

// Notifier announces progress on a user's devices. Best effort; it never
// fails the action that earned the points.
type Notifier interface {
	BadgeEarned(ctx context.Context, to uuid.UUID, title, description string)
	PassedOnLeaderboard(ctx context.Context, to uuid.UUID, by *socialv1.PublicUser)
}

type noopNotifier struct{}

func (noopNotifier) BadgeEarned(context.Context, uuid.UUID, string, string)               {}
func (noopNotifier) PassedOnLeaderboard(context.Context, uuid.UUID, *socialv1.PublicUser) {}

// Quiet hours: no progress push is sent from 22:00 to 08:00 in the
// recipient's zone.
const (
	quietFrom = 22
	quietTo   = 8
)

// Service is the points layer's logic.
type Service struct {
	repo    Repository
	graph   Graph
	notify  Notifier
	log     *slog.Logger
	now     func() time.Time
	enabled bool
}

// NewService builds the service. With enabled false every award is a no-op
// and the reads return empty progress: the kill switch.
func NewService(repo Repository, graph Graph, notify Notifier, log *slog.Logger, enabled bool) *Service {
	if notify == nil {
		notify = noopNotifier{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:    repo,
		graph:   graph,
		notify:  notify,
		log:     log.With(slog.String("component", "gamification")),
		now:     time.Now,
		enabled: enabled,
	}
}

// Result is what one award did.
type Result struct {
	Points    int
	Totals    Totals
	NewBadges []Badge
}

// Award scores one action. It is safe to call from any code path that records
// the action: a repeat of the same award, or one past the kind's daily cap,
// awards nothing. Failures are returned, and callers on a user's request path
// should log them rather than fail the request (see AwardQuietly).
func (s *Service) Award(ctx context.Context, a Award) (Result, error) {
	if !s.enabled || a.UserID == uuid.Nil {
		return Result{}, nil
	}
	rule, ok := Rules[a.Kind]
	if !ok {
		return Result{}, fmt.Errorf("%w: unknown kind %d", ErrInvalid, a.Kind)
	}
	before, err := s.repo.Totals(ctx, a.UserID)
	if err != nil {
		return Result{}, err
	}
	today := LocalDate(s.now(), before.Timezone)
	inserted, totals, err := s.repo.Insert(ctx, Event{
		UserID:    a.UserID,
		Kind:      a.Kind,
		RefKey:    a.RefKey,
		Points:    rule.Points,
		Label:     a.Label,
		LocalDate: today,
	}, rule.DailyCap, a.Kind == KindDailyCheckIn)
	if err != nil {
		return Result{}, err
	}
	if !inserted {
		return Result{Totals: totals}, nil
	}
	res := Result{Points: rule.Points, Totals: totals}
	if res.NewBadges, err = s.grantBadges(ctx, a.UserID, totals); err != nil {
		s.log.Warn("badge check failed", slog.String("user_id", a.UserID.String()), slog.Any("error", err))
	}
	s.announcePass(ctx, a.UserID, rule.Points, today)
	return res, nil
}

// AwardQuietly is Award for side effects: it logs instead of returning, so
// the action that earned the points never fails because of them.
func (s *Service) AwardQuietly(ctx context.Context, a Award) int {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	res, err := s.Award(ctx, a)
	if err != nil {
		s.log.Warn("award failed",
			slog.String("user_id", a.UserID.String()),
			slog.Int("kind", int(a.Kind)),
			slog.Any("error", err))
		return 0
	}
	return res.Points
}

func (s *Service) grantBadges(ctx context.Context, userID uuid.UUID, totals Totals) ([]Badge, error) {
	counts, err := s.repo.Counts(ctx, userID, totals.LongestStreak)
	if err != nil {
		return nil, err
	}
	var earned []Badge
	for _, b := range Badges {
		if !b.Earned(counts) {
			continue
		}
		granted, err := s.repo.GrantBadge(ctx, userID, b.ID)
		if err != nil {
			return earned, err
		}
		if granted {
			earned = append(earned, b)
			s.notifyIfAwake(ctx, userID, totals.Timezone, func(ctx context.Context) {
				s.notify.BadgeEarned(ctx, userID, b.Title, b.Description)
			})
		}
	}
	return earned, nil
}

// announcePass tells each friend whose weekly total userID just overtook.
// Each friend hears at most once a day, never at night.
func (s *Service) announcePass(ctx context.Context, userID uuid.UUID, points int, today time.Time) {
	if s.graph == nil || points <= 0 {
		return
	}
	friends, err := s.graph.FriendIDs(ctx, userID)
	if err != nil || len(friends) == 0 {
		return
	}
	ids := append([]uuid.UUID{userID}, friends...)
	week := PeriodFor(PeriodWeek, today)
	scores, err := s.repo.Scores(ctx, ids, MetricPoints, week)
	if err != nil {
		s.log.Debug("pass check skipped", slog.Any("error", err))
		return
	}
	after := scores[userID]
	before := after - int64(points)
	var passed []uuid.UUID
	for _, f := range friends {
		if v := scores[f]; v >= before && v < after && v > 0 {
			passed = append(passed, f)
		}
	}
	if len(passed) == 0 {
		return
	}
	cards, err := s.graph.PublicUsers(ctx, []uuid.UUID{userID})
	if err != nil {
		return
	}
	progresses, err := s.repo.Progresses(ctx, passed)
	if err != nil {
		return
	}
	for _, f := range passed {
		tz := progresses[f].Timezone
		if tz == "" {
			tz = "UTC"
		}
		ok, err := s.repo.ClaimPushSlot(ctx, f, "passed", LocalDate(s.now(), tz))
		if err != nil || !ok {
			continue
		}
		friend := f
		s.notifyIfAwake(ctx, friend, tz, func(ctx context.Context) {
			s.notify.PassedOnLeaderboard(ctx, friend, cards[userID])
		})
	}
}

func (s *Service) notifyIfAwake(ctx context.Context, to uuid.UUID, tz string, send func(context.Context)) {
	h := LocalHour(s.now(), tz)
	if h >= quietFrom || h < quietTo {
		return
	}
	send(ctx)
}

// Progress is a user's progress as the API returns it.
type Progress struct {
	Totals      Totals
	Level       int
	ToNext      int64
	Earned      map[string]time.Time
	CheckedIn   bool
	Searched    bool
	PlacesToday int
}

// MyProgress loads userID's progress.
func (s *Service) MyProgress(ctx context.Context, userID uuid.UUID) (*Progress, error) {
	totals, err := s.repo.Totals(ctx, userID)
	if err != nil {
		return nil, err
	}
	p := &Progress{Totals: totals, Earned: map[string]time.Time{}}
	p.Level, p.ToNext = LevelFor(totals.TotalPoints)
	if !s.enabled {
		return p, nil
	}
	if p.Earned, err = s.repo.Badges(ctx, userID); err != nil {
		return nil, err
	}
	today, err := s.repo.Today(ctx, userID, LocalDate(s.now(), totals.Timezone))
	if err != nil {
		return nil, err
	}
	p.CheckedIn = today[KindDailyCheckIn] > 0
	p.Searched = today[KindDailySearch] > 0
	p.PlacesToday = today[KindPlaceVisited]
	// A streak whose last day is before yesterday is already broken; show 0
	// rather than a number the next check-in will reset.
	if totals.LastActiveDate != nil && daysBetween(*totals.LastActiveDate, LocalDate(s.now(), totals.Timezone)) > 1 {
		p.Totals.CurrentStreak = 0
	}
	return p, nil
}

// CheckIn marks userID active today in tz. Idempotent per local date.
func (s *Service) CheckIn(ctx context.Context, userID uuid.UUID, tz string) (Result, error) {
	if !ValidTimezone(tz) {
		return Result{}, fmt.Errorf("%w: unknown timezone %q", ErrInvalid, tz)
	}
	if err := s.repo.SetTimezone(ctx, userID, tz); err != nil {
		return Result{}, err
	}
	today := LocalDate(s.now(), tz).Format("2006-01-02")
	return s.Award(ctx, Award{UserID: userID, Kind: KindDailyCheckIn, RefKey: "checkin:" + today, Label: "Daily check-in"})
}

// SearchedToday awards the first search of the user's day.
func (s *Service) SearchedToday(ctx context.Context, userID uuid.UUID) {
	if userID == uuid.Nil || !s.enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	totals, err := s.repo.Totals(ctx, userID)
	if err != nil {
		s.log.Debug("daily search skipped", slog.Any("error", err))
		return
	}
	today := LocalDate(s.now(), totals.Timezone).Format("2006-01-02")
	s.AwardQuietly(ctx, Award{UserID: userID, Kind: KindDailySearch, RefKey: "search:" + today, Label: "First search of the day"})
}

// Visit is a place visit to score.
type Visit struct {
	UserID uuid.UUID
	POIID  string
	// CityKey identifies the visited city row ("" when the city is not new).
	NewCityKey string
	CityName   string
	CityLat    float64
	CityLon    float64
	Fix        *Fix
}

// ScoreVisit awards a place visited on the spot and, when the visit created
// the city on the user's globe, a new city. Without a fresh fix close to the
// place (or to the city), the visit scores nothing. Returns the points.
func (s *Service) ScoreVisit(ctx context.Context, v Visit) int {
	if !s.enabled || v.Fix == nil || !v.Fix.Fresh(s.now()) {
		return 0
	}
	total := 0
	if v.POIID != "" {
		lat, lon, name, found, err := s.repo.POI(ctx, v.POIID)
		if err != nil {
			s.log.Debug("visit scoring skipped", slog.Any("error", err))
		}
		if found && DistanceM(v.Fix.Latitude, v.Fix.Longitude, lat, lon) <= MaxVisitDistanceM {
			today := LocalDate(s.now(), s.timezone(ctx, v.UserID)).Format("2006-01-02")
			total += s.AwardQuietly(ctx, Award{
				UserID: v.UserID, Kind: KindPlaceVisited,
				RefKey: "poi:" + v.POIID + ":" + today, Label: "Visited " + name,
			})
		}
	}
	if v.NewCityKey != "" && DistanceM(v.Fix.Latitude, v.Fix.Longitude, v.CityLat, v.CityLon) <= MaxCityDistanceKm*1000 {
		total += s.AwardQuietly(ctx, Award{
			UserID: v.UserID, Kind: KindNewCity,
			RefKey: "city:" + v.NewCityKey, Label: "New city: " + v.CityName,
		})
	}
	return total
}

func (s *Service) timezone(ctx context.Context, userID uuid.UUID) string {
	t, err := s.repo.Totals(ctx, userID)
	if err != nil {
		return "UTC"
	}
	return t.Timezone
}

// DayResult is what completing a trip day did.
type DayResult struct {
	Result
	TripCompleted bool
}

// CompleteTripDay records a walked day of the caller's own trip and scores
// it, and the whole trip when every day is done.
func (s *Service) CompleteTripDay(ctx context.Context, userID uuid.UUID, tripID, dayID string, stopsDone int, tz string) (DayResult, error) {
	if stopsDone < 1 {
		return DayResult{}, fmt.Errorf("%w: a day with no stops reached does not count", ErrInvalid)
	}
	if ValidTimezone(tz) {
		if err := s.repo.SetTimezone(ctx, userID, tz); err != nil {
			return DayResult{}, err
		}
	}
	_, tripDone, label, err := s.repo.CompleteDay(ctx, userID, tripID, dayID)
	if err != nil {
		return DayResult{}, err
	}
	out := DayResult{TripCompleted: tripDone}
	day, err := s.Award(ctx, Award{UserID: userID, Kind: KindTripDayCompleted, RefKey: "tripday:" + dayID, Label: "Walked " + label})
	if err != nil {
		return DayResult{}, err
	}
	out.Points, out.Totals, out.NewBadges = day.Points, day.Totals, day.NewBadges
	if tripDone {
		trip, err := s.Award(ctx, Award{UserID: userID, Kind: KindTripCompleted, RefKey: "trip:" + tripID, Label: "Finished a trip"})
		if err != nil {
			return DayResult{}, err
		}
		out.Points += trip.Points
		if trip.Points > 0 {
			out.Totals = trip.Totals
		}
		out.NewBadges = append(out.NewBadges, trip.NewBadges...)
	}
	return out, nil
}

// Entry is one leaderboard row.
type Entry struct {
	User   *socialv1.PublicUser
	UserID uuid.UUID
	Rank   int
	Value  int64
	Totals Totals
	IsMe   bool
}

// Leaderboard ranks userID and their friends for metric over the period
// containing today in the caller's zone. Friends who hid themselves are left
// out; the caller is always in. Blocks end friendships, so blocked users are
// never in the friend list.
func (s *Service) Leaderboard(ctx context.Context, userID uuid.UUID, kind PeriodKind, metric Metric) ([]Entry, Period, error) {
	totals, err := s.repo.Totals(ctx, userID)
	if err != nil {
		return nil, Period{}, err
	}
	period := PeriodFor(kind, LocalDate(s.now(), totals.Timezone))
	ids := []uuid.UUID{userID}
	if s.graph != nil {
		friends, err := s.graph.FriendIDs(ctx, userID)
		if err != nil {
			return nil, Period{}, err
		}
		if len(friends) > 0 {
			visible, err := s.repo.Visible(ctx, friends)
			if err != nil {
				return nil, Period{}, err
			}
			for _, f := range friends {
				if visible[f] {
					ids = append(ids, f)
				}
			}
		}
	}
	scores, err := s.repo.Scores(ctx, ids, metric, period)
	if err != nil {
		return nil, Period{}, err
	}
	progresses, err := s.repo.Progresses(ctx, ids)
	if err != nil {
		return nil, Period{}, err
	}
	cards := map[uuid.UUID]*socialv1.PublicUser{}
	if s.graph != nil {
		if cards, err = s.graph.PublicUsers(ctx, ids); err != nil {
			return nil, Period{}, err
		}
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		if id != userID && cards[id] == nil {
			continue // deactivated or gone
		}
		entries = append(entries, Entry{User: cards[id], UserID: id, Value: scores[id], Totals: progresses[id], IsMe: id == userID})
	}
	Rank(entries)
	return entries, period, nil
}

// Rank sorts entries by value, highest first, and gives equal values the same
// rank (1, 1, 3). Ties list the caller first, then by user id, so the order
// is stable between calls.
func Rank(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Value != entries[j].Value {
			return entries[i].Value > entries[j].Value
		}
		if entries[i].IsMe != entries[j].IsMe {
			return entries[i].IsMe
		}
		return entries[i].UserID.String() < entries[j].UserID.String()
	})
	for i := range entries {
		if i > 0 && entries[i].Value == entries[i-1].Value {
			entries[i].Rank = entries[i-1].Rank
		} else {
			entries[i].Rank = i + 1
		}
	}
}

// History is a page of the ledger.
func (s *Service) History(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return s.repo.History(ctx, userID, limit, before)
}
