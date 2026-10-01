package gamification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

// fakeRepo keeps the ledger in memory with the same semantics as the
// Postgres repository: unique (user, kind, ref), daily caps, streaks.
type fakeRepo struct {
	mu      sync.Mutex
	events  []Event
	totals  map[uuid.UUID]Totals
	badges  map[uuid.UUID]map[string]time.Time
	hidden  map[uuid.UUID]bool
	days    map[string]*fakeDay // dayID → day
	pois    map[string][2]float64
	pushLog map[string]bool
}

type fakeDay struct {
	owner  uuid.UUID
	tripID string
	done   bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		totals:  map[uuid.UUID]Totals{},
		badges:  map[uuid.UUID]map[string]time.Time{},
		hidden:  map[uuid.UUID]bool{},
		days:    map[string]*fakeDay{},
		pois:    map[string][2]float64{},
		pushLog: map[string]bool{},
	}
}

func (r *fakeRepo) Totals(_ context.Context, id uuid.UUID) (Totals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.totals[id]
	if !ok {
		t.Timezone = "UTC"
	}
	return t, nil
}

func (r *fakeRepo) SetTimezone(_ context.Context, id uuid.UUID, tz string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.totals[id]
	t.Timezone = tz
	r.totals[id] = t
	return nil
}

func (r *fakeRepo) Insert(_ context.Context, e Event, dailyCap int, bump bool) (bool, Totals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.totals[e.UserID]
	if !ok {
		t.Timezone = "UTC"
	}
	if dailyCap > 0 {
		n := 0
		for _, x := range r.events {
			if x.UserID == e.UserID && x.Kind == e.Kind && x.LocalDate.Equal(e.LocalDate) {
				n++
			}
		}
		if n >= dailyCap {
			return false, t, nil
		}
	}
	for _, x := range r.events {
		if x.UserID == e.UserID && x.Kind == e.Kind && x.RefKey == e.RefKey {
			return false, t, nil
		}
	}
	e.ID = uuid.New()
	e.CreatedAt = time.Now()
	r.events = append(r.events, e)
	t.TotalPoints += int64(e.Points)
	if bump {
		t.CurrentStreak = NextStreak(t.CurrentStreak, t.LastActiveDate, e.LocalDate)
		if t.CurrentStreak > t.LongestStreak {
			t.LongestStreak = t.CurrentStreak
		}
		d := e.LocalDate
		t.LastActiveDate = &d
	}
	r.totals[e.UserID] = t
	return true, t, nil
}

func (r *fakeRepo) Counts(_ context.Context, id uuid.UUID, longest int) (Counts, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := Counts{LongestStreak: longest}
	places := map[string]bool{}
	for _, e := range r.events {
		if e.UserID != id {
			continue
		}
		switch e.Kind {
		case KindNewCity:
			c.Cities++
		case KindPlaceVisited:
			places[strings.Split(e.RefKey, ":")[1]] = true
		case KindScoutClaim:
			c.ScoutClaims++
		case KindTripCompleted:
			c.TripsDone++
		}
	}
	c.Places = len(places)
	return c, nil
}

func (r *fakeRepo) Badges(_ context.Context, id uuid.UUID) (map[string]time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]time.Time{}
	for k, v := range r.badges[id] {
		out[k] = v
	}
	return out, nil
}

func (r *fakeRepo) GrantBadge(_ context.Context, id uuid.UUID, badge string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.badges[id] == nil {
		r.badges[id] = map[string]time.Time{}
	}
	if _, ok := r.badges[id][badge]; ok {
		return false, nil
	}
	r.badges[id][badge] = time.Now()
	return true, nil
}

func (r *fakeRepo) Today(_ context.Context, id uuid.UUID, d time.Time) (map[Kind]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[Kind]int{}
	for _, e := range r.events {
		if e.UserID == id && e.LocalDate.Equal(d) {
			out[e.Kind]++
		}
	}
	return out, nil
}

func (r *fakeRepo) Visible(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		out[id] = !r.hidden[id]
	}
	return out, nil
}

func (r *fakeRepo) Scores(_ context.Context, ids []uuid.UUID, m Metric, p Period) (map[uuid.UUID]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[uuid.UUID]int64{}
	for _, id := range ids {
		out[id] = 0
	}
	for _, e := range r.events {
		if _, ok := out[e.UserID]; !ok {
			continue
		}
		if !p.From.IsZero() && (e.LocalDate.Before(p.From) || e.LocalDate.After(p.To)) {
			continue
		}
		switch m {
		case MetricPoints:
			out[e.UserID] += int64(e.Points)
		case MetricCities:
			if e.Kind == KindNewCity {
				out[e.UserID]++
			}
		case MetricPlaces:
			if e.Kind == KindPlaceVisited {
				out[e.UserID]++
			}
		}
	}
	return out, nil
}

func (r *fakeRepo) Progresses(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]Totals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[uuid.UUID]Totals{}
	for _, id := range ids {
		if t, ok := r.totals[id]; ok {
			out[id] = t
		}
	}
	return out, nil
}

func (r *fakeRepo) History(_ context.Context, id uuid.UUID, limit int, _ time.Time) ([]Event, error) {
	var out []Event
	for i := len(r.events) - 1; i >= 0 && len(out) < limit; i-- {
		if r.events[i].UserID == id {
			out = append(out, r.events[i])
		}
	}
	return out, nil
}

func (r *fakeRepo) CompleteDay(_ context.Context, id uuid.UUID, tripID, dayID string) (bool, bool, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.days[dayID]
	if !ok || d.owner != id || d.tripID != tripID {
		return false, false, "", ErrNotFound
	}
	newly := !d.done
	d.done = true
	all := true
	for _, x := range r.days {
		if x.tripID == tripID && !x.done {
			all = false
		}
	}
	return newly, all, "Rome, day 1", nil
}

func (r *fakeRepo) POI(_ context.Context, id string) (float64, float64, string, bool, error) {
	p, ok := r.pois[id]
	return p[0], p[1], "Pantheon", ok, nil
}

func (r *fakeRepo) ClaimPushSlot(_ context.Context, id uuid.UUID, kind string, d time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := id.String() + kind + d.Format("2006-01-02")
	if r.pushLog[k] {
		return false, nil
	}
	r.pushLog[k] = true
	return true, nil
}

type fakeGraph struct {
	friends map[uuid.UUID][]uuid.UUID
}

func (g fakeGraph) FriendIDs(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	return g.friends[id], nil
}

func (g fakeGraph) PublicUsers(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	out := map[uuid.UUID]*socialv1.PublicUser{}
	for _, id := range ids {
		out[id] = &socialv1.PublicUser{Id: id.String(), Username: "u" + id.String()[:4]}
	}
	return out, nil
}

type recordingNotifier struct {
	mu     sync.Mutex
	badges []string
	passed []uuid.UUID
}

func (n *recordingNotifier) BadgeEarned(_ context.Context, _ uuid.UUID, title, _ string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.badges = append(n.badges, title)
}

func (n *recordingNotifier) PassedOnLeaderboard(_ context.Context, to uuid.UUID, _ *socialv1.PublicUser) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.passed = append(n.passed, to)
}

// noon on Thursday 2026-10-01 UTC: awake everywhere near Europe.
var noon = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newTestService(repo *fakeRepo, graph Graph, n Notifier) *Service {
	s := NewService(repo, graph, n, nil, true)
	s.now = func() time.Time { return noon }
	return s
}

func TestLevelFor(t *testing.T) {
	cases := []struct {
		total  int64
		level  int
		toNext int64
	}{
		{0, 1, 100}, {99, 1, 1}, {100, 2, 200}, {299, 2, 1}, {300, 3, 300}, {1000, 5, 500},
	}
	for _, c := range cases {
		level, toNext := LevelFor(c.total)
		if level != c.level || toNext != c.toNext {
			t.Errorf("LevelFor(%d) = %d, %d; want %d, %d", c.total, level, toNext, c.level, c.toNext)
		}
	}
}

func TestNextStreak(t *testing.T) {
	day := func(d int) *time.Time { x := time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC); return &x }
	today := *day(10)
	if got := NextStreak(0, nil, today); got != 1 {
		t.Errorf("first check-in = %d, want 1", got)
	}
	if got := NextStreak(4, day(9), today); got != 5 {
		t.Errorf("after yesterday = %d, want 5", got)
	}
	if got := NextStreak(4, day(10), today); got != 4 {
		t.Errorf("same day = %d, want 4", got)
	}
	if got := NextStreak(4, day(7), today); got != 1 {
		t.Errorf("after a gap = %d, want 1", got)
	}
}

func TestPeriodForWeekStartsMonday(t *testing.T) {
	thursday := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p := PeriodFor(PeriodWeek, thursday)
	if p.From.Weekday() != time.Monday || p.From.Day() != 28 || p.To.Day() != 4 {
		t.Fatalf("week = %v..%v", p.From, p.To)
	}
	sunday := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	if q := PeriodFor(PeriodWeek, sunday); !q.From.Equal(p.From) {
		t.Fatalf("Sunday belongs to the week starting %v, got %v", p.From, q.From)
	}
	m := PeriodFor(PeriodMonth, thursday)
	if m.From.Day() != 1 || m.To.Day() != 31 {
		t.Fatalf("month = %v..%v", m.From, m.To)
	}
}

func TestLocalDateFollowsTimezone(t *testing.T) {
	lateUTC := time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC)
	if got := LocalDate(lateUTC, "Asia/Tokyo"); got.Day() != 2 {
		t.Errorf("Tokyo date = %v, want the 2nd", got)
	}
	if got := LocalDate(lateUTC, "Nowhere/Invalid"); got.Day() != 1 {
		t.Errorf("unknown zone falls back to UTC, got %v", got)
	}
}

func TestRankSharesTies(t *testing.T) {
	me := uuid.New()
	entries := []Entry{
		{UserID: uuid.New(), Value: 10},
		{UserID: me, Value: 30, IsMe: true},
		{UserID: uuid.New(), Value: 30},
		{UserID: uuid.New(), Value: 5},
	}
	Rank(entries)
	got := []int{entries[0].Rank, entries[1].Rank, entries[2].Rank, entries[3].Rank}
	want := []int{1, 1, 3, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranks = %v, want %v", got, want)
		}
	}
	if !entries[0].IsMe {
		t.Fatalf("a tie lists the caller first")
	}
}

func TestCheckInIsIdempotentAndBuildsAStreak(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	ctx := context.Background()

	first, err := s.CheckIn(ctx, u, "Europe/Lisbon")
	if err != nil || first.Points != Rules[KindDailyCheckIn].Points {
		t.Fatalf("first check-in = %+v, %v", first, err)
	}
	again, err := s.CheckIn(ctx, u, "Europe/Lisbon")
	if err != nil || again.Points != 0 {
		t.Fatalf("second check-in the same day must award nothing, got %+v, %v", again, err)
	}
	s.now = func() time.Time { return noon.AddDate(0, 0, 1) }
	next, err := s.CheckIn(ctx, u, "Europe/Lisbon")
	if err != nil || next.Totals.CurrentStreak != 2 {
		t.Fatalf("next day streak = %+v, %v", next.Totals, err)
	}
}

func TestCheckInRejectsUnknownTimezone(t *testing.T) {
	s := newTestService(newFakeRepo(), nil, nil)
	if _, err := s.CheckIn(context.Background(), uuid.New(), "Mars/Olympus"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestPlaceVisitsAreCappedPerDay(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	ctx := context.Background()
	limit := Rules[KindPlaceVisited].DailyCap
	for i := 0; i < limit+5; i++ {
		_, err := s.Award(ctx, Award{UserID: u, Kind: KindPlaceVisited, RefKey: "poi:" + uuid.NewString() + ":2026-10-01"})
		if err != nil {
			t.Fatal(err)
		}
	}
	tot, _ := repo.Totals(ctx, u)
	if want := int64(limit * Rules[KindPlaceVisited].Points); tot.TotalPoints != want {
		t.Fatalf("total = %d, want %d (cap of %d)", tot.TotalPoints, want, limit)
	}
}

func TestScoreVisitNeedsAFreshFixNearThePlace(t *testing.T) {
	repo := newFakeRepo()
	repo.pois["pantheon"] = [2]float64{41.8986, 12.4769}
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	ctx := context.Background()
	near := &Fix{Latitude: 41.8990, Longitude: 12.4771, AccuracyM: 20, ObservedAt: noon.Add(-time.Minute)}
	far := &Fix{Latitude: 41.9100, Longitude: 12.4800, AccuracyM: 20, ObservedAt: noon.Add(-time.Minute)}
	stale := &Fix{Latitude: 41.8990, Longitude: 12.4771, AccuracyM: 20, ObservedAt: noon.Add(-time.Hour)}
	vague := &Fix{Latitude: 41.8990, Longitude: 12.4771, AccuracyM: 900, ObservedAt: noon}

	for name, fix := range map[string]*Fix{"none": nil, "far": far, "stale": stale, "vague": vague} {
		if got := s.ScoreVisit(ctx, Visit{UserID: u, POIID: "pantheon", Fix: fix}); got != 0 {
			t.Errorf("%s fix scored %d, want 0", name, got)
		}
	}
	if got := s.ScoreVisit(ctx, Visit{UserID: u, POIID: "pantheon", Fix: near}); got != Rules[KindPlaceVisited].Points {
		t.Errorf("near fix scored %d", got)
	}
	if got := s.ScoreVisit(ctx, Visit{UserID: u, POIID: "pantheon", Fix: near}); got != 0 {
		t.Errorf("the same place twice in a day scored %d, want 0", got)
	}
	city := s.ScoreVisit(ctx, Visit{UserID: u, NewCityKey: "rome-row", CityName: "Rome", CityLat: 41.9028, CityLon: 12.4964, Fix: near})
	if city != Rules[KindNewCity].Points {
		t.Errorf("new city scored %d", city)
	}
}

func TestCompleteTripDayScoresTheDayAndTheTrip(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	trip := uuid.NewString()
	repo.days["d1"] = &fakeDay{owner: u, tripID: trip}
	repo.days["d2"] = &fakeDay{owner: u, tripID: trip}
	ctx := context.Background()

	if _, err := s.CompleteTripDay(ctx, u, trip, "d1", 0, "UTC"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no stops: err = %v", err)
	}
	if _, err := s.CompleteTripDay(ctx, uuid.New(), trip, "d1", 3, "UTC"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's day: err = %v", err)
	}
	one, err := s.CompleteTripDay(ctx, u, trip, "d1", 3, "UTC")
	if err != nil || one.TripCompleted || one.Points != Rules[KindTripDayCompleted].Points {
		t.Fatalf("day one = %+v, %v", one, err)
	}
	two, err := s.CompleteTripDay(ctx, u, trip, "d2", 2, "UTC")
	want := Rules[KindTripDayCompleted].Points + Rules[KindTripCompleted].Points
	if err != nil || !two.TripCompleted || two.Points != want {
		t.Fatalf("last day = %+v, %v; want %d points and a finished trip", two, err, want)
	}
	found := false
	for _, b := range two.NewBadges {
		found = found || b.ID == "trip-finisher"
	}
	if !found {
		t.Fatalf("finishing a trip earns trip-finisher, got %+v", two.NewBadges)
	}
	again, _ := s.CompleteTripDay(ctx, u, trip, "d2", 2, "UTC")
	if again.Points != 0 {
		t.Fatalf("completing a day twice scored %d", again.Points)
	}
}

func TestLeaderboardIsFriendsOnlyAndHonoursVisibility(t *testing.T) {
	repo := newFakeRepo()
	me, friend, hidden, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	graph := fakeGraph{friends: map[uuid.UUID][]uuid.UUID{me: {friend, hidden}}}
	repo.hidden[hidden] = true
	s := newTestService(repo, graph, nil)
	ctx := context.Background()
	for _, u := range []uuid.UUID{friend, hidden, stranger} {
		if _, err := s.Award(ctx, Award{UserID: u, Kind: KindNewCity, RefKey: "city:" + u.String()}); err != nil {
			t.Fatal(err)
		}
	}

	entries, period, err := s.Leaderboard(ctx, me, PeriodWeek, MetricPoints)
	if err != nil {
		t.Fatal(err)
	}
	if period.From.IsZero() {
		t.Fatalf("a week has bounds")
	}
	ids := map[uuid.UUID]bool{}
	for _, e := range entries {
		ids[e.UserID] = true
	}
	if !ids[me] || !ids[friend] || ids[hidden] || ids[stranger] {
		t.Fatalf("leaderboard ids = %v; want me and the visible friend only", ids)
	}
	if entries[0].UserID != friend || entries[0].Rank != 1 || entries[1].Rank != 2 {
		t.Fatalf("order = %+v", entries)
	}
}

func TestOvertakingAFriendNotifiesThemOnceADay(t *testing.T) {
	repo := newFakeRepo()
	me, friend := uuid.New(), uuid.New()
	graph := fakeGraph{friends: map[uuid.UUID][]uuid.UUID{me: {friend}, friend: {me}}}
	n := &recordingNotifier{}
	s := newTestService(repo, graph, n)
	ctx := context.Background()

	if _, err := s.Award(ctx, Award{UserID: friend, Kind: KindTripDayCompleted, RefKey: "tripday:x"}); err != nil {
		t.Fatal(err)
	}
	// 20 points to beat: a 50-point city overtakes.
	if _, err := s.Award(ctx, Award{UserID: me, Kind: KindNewCity, RefKey: "city:a"}); err != nil {
		t.Fatal(err)
	}
	if len(n.passed) != 1 || n.passed[0] != friend {
		t.Fatalf("passed = %v, want the friend once", n.passed)
	}
	if _, err := s.Award(ctx, Award{UserID: friend, Kind: KindTripCompleted, RefKey: "trip:x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Award(ctx, Award{UserID: me, Kind: KindTripCompleted, RefKey: "trip:y"}); err != nil {
		t.Fatal(err)
	}
	if len(n.passed) != 2 || n.passed[1] != me {
		t.Fatalf("the friend overtook me back: passed = %v", n.passed)
	}
	if _, err := s.Award(ctx, Award{UserID: me, Kind: KindNewCity, RefKey: "city:b"}); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, p := range n.passed {
		if p == friend {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the friend heard %d times today, want once", count)
	}
}

func TestNoProgressPushAtNight(t *testing.T) {
	repo := newFakeRepo()
	n := &recordingNotifier{}
	s := newTestService(repo, nil, n)
	s.now = func() time.Time { return time.Date(2026, 10, 1, 23, 0, 0, 0, time.UTC) }
	u := uuid.New()
	if _, err := s.Award(context.Background(), Award{UserID: u, Kind: KindNewCity, RefKey: "city:night"}); err != nil {
		t.Fatal(err)
	}
	if len(n.badges) != 0 {
		t.Fatalf("badge push at 23:00: %v", n.badges)
	}
	if b, _ := repo.Badges(context.Background(), u); b["first-city"].IsZero() {
		t.Fatalf("the badge is still granted at night")
	}
}

func TestKillSwitchAwardsNothing(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, nil, nil, nil, false)
	res, err := s.CheckIn(context.Background(), uuid.New(), "UTC")
	if err != nil || res.Points != 0 || len(repo.events) != 0 {
		t.Fatalf("disabled check-in = %+v, %v, %d events", res, err, len(repo.events))
	}
}
