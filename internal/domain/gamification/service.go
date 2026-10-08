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

// Neighborhoods names the neighborhood a coordinate sits in ("" when there
// is none). localcontext.BigDataCloudGeocoder satisfies it.
type Neighborhoods interface {
	Neighborhood(ctx context.Context, lat, lon float64) (string, error)
}

// PlanChecker is the caller's plan, for the one field feature a plan may
// unlock (past weeks' boards). subscription.Service satisfies it.
type PlanChecker interface {
	EffectivePlan(ctx context.Context, userID uuid.UUID) (string, error)
}

// Service is the points layer's logic.
type Service struct {
	repo    Repository
	graph   Graph
	notify  Notifier
	log     *slog.Logger
	now     func() time.Time
	enabled bool

	overallRanks Thresholds
	cityRanks    Thresholds
	hoods        Neighborhoods
	plans        PlanChecker
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

		overallRanks: DefaultOverallThresholds,
		cityRanks:    DefaultCityThresholds,
	}
}

// WithRanks sets the rank thresholds, overall and per city.
func (s *Service) WithRanks(overall, city Thresholds) *Service {
	s.overallRanks, s.cityRanks = overall, city
	return s
}

// WithNeighborhoods turns on the first-neighborhood award.
func (s *Service) WithNeighborhoods(n Neighborhoods) *Service {
	s.hoods = n
	return s
}

// WithPlans lets past weeks' boards follow plan gating. Without it they are
// open to everyone, as they are while gating is off.
func (s *Service) WithPlans(p PlanChecker) *Service {
	s.plans = p
	return s
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
	if rule.Retired {
		return Result{}, nil
	}
	before, err := s.repo.Totals(ctx, a.UserID)
	if err != nil {
		return Result{}, err
	}
	today := LocalDate(s.now(), before.Timezone)
	inserted, totals, err := s.repo.Insert(ctx, Event{
		UserID:      a.UserID,
		Kind:        a.Kind,
		RefKey:      a.RefKey,
		Points:      rule.Points,
		FieldPoints: rule.Points,
		Label:       a.Label,
		LocalDate:   today,
		SeasonID:    SeasonID(today),
		CityID:      a.CityID,
	}, rule.DailyCap)
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
	// Opening the app and searching no longer earn anything. Older apps list
	// them as today's to-dos; reporting them done keeps those apps from
	// nudging people toward points that no longer exist.
	p.CheckedIn = true
	p.Searched = true
	p.PlacesToday = today[KindPlaceVisited]
	// A streak whose last day is before yesterday is already broken; show 0
	// rather than a number the next check-in will reset.
	if totals.LastActiveDate != nil && daysBetween(*totals.LastActiveDate, LocalDate(s.now(), totals.Timezone)) > 1 {
		p.Totals.CurrentStreak = 0
	}
	return p, nil
}

// CheckIn records the zone the device reports, which decides the user's
// local date and so their week. It awards nothing: opening the app is not
// exploration.
func (s *Service) CheckIn(ctx context.Context, userID uuid.UUID, tz string) (Result, error) {
	if !ValidTimezone(tz) {
		return Result{}, fmt.Errorf("%w: unknown timezone %q", ErrInvalid, tz)
	}
	if !s.enabled {
		return Result{}, nil
	}
	if err := s.repo.SetTimezone(ctx, userID, tz); err != nil {
		return Result{}, err
	}
	return Result{}, nil
}

// Visit is a place visit to score.
type Visit struct {
	UserID uuid.UUID
	POIID  string
	// NewCityKey identifies the city ("" when the visit did not put a new
	// city on the user's globe): the shared cities row when known, else the
	// visited-city row.
	NewCityKey string
	NewCityID  *uuid.UUID
	CityName   string
	CityLat    float64
	CityLon    float64
	Fix        *Fix
}

// ScoreVisit awards a place visited on the spot, the first neighborhood it
// reaches, and, when the visit created the city on the user's globe, a new
// city. Without a fresh fix close to the place (or to the city), the visit
// scores nothing. Returns the points.
func (s *Service) ScoreVisit(ctx context.Context, v Visit) int {
	if !s.enabled || v.Fix == nil || !v.Fix.Fresh(s.now()) {
		return 0
	}
	total := 0
	if v.POIID != "" {
		place, err := s.repo.Place(ctx, v.POIID)
		if err != nil {
			s.log.Debug("visit scoring skipped", slog.Any("error", err))
		}
		if place.HasLocation && DistanceM(v.Fix.Latitude, v.Fix.Longitude, place.Lat, place.Lon) <= MaxVisitDistanceM {
			today := LocalDate(s.now(), s.timezone(ctx, v.UserID)).Format("2006-01-02")
			total += s.AwardQuietly(ctx, Award{
				UserID: v.UserID, Kind: KindPlaceVisited,
				RefKey: "poi:" + v.POIID + ":" + today, Label: "Visited " + place.Name,
				CityID: place.CityID,
			})
			total += s.firstNeighborhood(ctx, v.UserID, v.POIID, place)
		}
	}
	if v.NewCityKey != "" && DistanceM(v.Fix.Latitude, v.Fix.Longitude, v.CityLat, v.CityLon) <= MaxCityDistanceKm*1000 {
		total += s.AwardQuietly(ctx, Award{
			UserID: v.UserID, Kind: KindNewCity,
			RefKey: "city:" + v.NewCityKey, Label: "New city: " + v.CityName,
			CityID: v.NewCityID,
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
// it, and the whole trip when every day is done. Once any stop of the day
// has been marked (MarkStop), the marks decide and stopsDone is ignored;
// before that, older apps that only count stops are taken at their word.
func (s *Service) CompleteTripDay(ctx context.Context, userID uuid.UUID, tripID, dayID string, stopsDone int, tz string) (DayResult, error) {
	tid, err1 := uuid.Parse(tripID)
	did, err2 := uuid.Parse(dayID)
	if err1 != nil || err2 != nil {
		return DayResult{}, ErrNotFound
	}
	if ValidTimezone(tz) {
		if err := s.repo.SetTimezone(ctx, userID, tz); err != nil {
			return DayResult{}, err
		}
	}
	trip, err := s.repo.Trip(ctx, userID, tid)
	if err != nil {
		return DayResult{}, err
	}
	di := trip.dayIndex(did)
	if di < 0 {
		return DayResult{}, ErrNotFound
	}
	day := trip.Days[di]
	switch {
	case day.Marked():
		if !day.Finished() {
			return DayResult{}, nil
		}
	case stopsDone < 1:
		return DayResult{}, fmt.Errorf("%w: a day with no stops reached does not count", ErrInvalid)
	}
	return s.finishDay(ctx, userID, trip, di)
}

// finishDay stamps day di finished and awards it, and the trip when that was
// its last unfinished day. Both awards are keyed, so finishing twice pays once.
func (s *Service) finishDay(ctx context.Context, userID uuid.UUID, trip *TripState, di int) (DayResult, error) {
	day := &trip.Days[di]
	if _, err := s.repo.CompleteDay(ctx, day.ID); err != nil {
		return DayResult{}, err
	}
	if day.CompletedAt == nil {
		now := s.now()
		day.CompletedAt = &now
	}
	cityID, _ := trip.City(*day)
	res, err := s.Award(ctx, Award{
		UserID: userID, Kind: KindTripDayCompleted, RefKey: "tripday:" + day.ID.String(),
		Label: fmt.Sprintf("Finished %s, day %d", trip.Title, day.Number), CityID: cityID,
	})
	if err != nil {
		return DayResult{}, err
	}
	out := DayResult{Result: res}
	if trip.Done() {
		out.TripCompleted = true
		done, err := s.Award(ctx, Award{
			UserID: userID, Kind: KindTripCompleted, RefKey: "trip:" + trip.ID.String(),
			Label: "Finished " + trip.Title, CityID: trip.CityID,
		})
		if err != nil {
			return DayResult{}, err
		}
		out.Points += done.Points
		if done.Points > 0 {
			out.Totals = done.Totals
		}
		out.NewBadges = append(out.NewBadges, done.NewBadges...)
	}
	return out, nil
}

func (t *TripState) dayIndex(id uuid.UUID) int {
	for i := range t.Days {
		if t.Days[i].ID == id {
			return i
		}
	}
	return -1
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

// History is a page of the ledger. fieldOnly leaves out rows that count
// nothing toward the field score.
func (s *Service) History(ctx context.Context, userID uuid.UUID, limit int, before time.Time, fieldOnly bool) ([]Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return s.repo.History(ctx, userID, limit, before, fieldOnly)
}
