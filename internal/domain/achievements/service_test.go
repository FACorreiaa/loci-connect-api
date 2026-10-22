package achievements

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	achievementv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/achievement"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/gamification"
)

type fakeRepo struct {
	mu         sync.Mutex
	counts     map[uuid.UUID]map[Counter]int64
	earned     map[uuid.UUID]map[Unit]Earned
	backfilled map[uuid.UUID]bool
	slots      map[string]bool
	rarity     Rarity
	tz         string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		counts:     map[uuid.UUID]map[Counter]int64{},
		earned:     map[uuid.UUID]map[Unit]Earned{},
		backfilled: map[uuid.UUID]bool{},
		slots:      map[string]bool{},
		tz:         "UTC",
	}
}

func (r *fakeRepo) Counters(_ context.Context, id uuid.UUID, _ int64) (map[Counter]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[Counter]int64{}
	for k, v := range r.counts[id] {
		out[k] = v
	}
	return out, nil
}

func (r *fakeRepo) Earned(_ context.Context, id uuid.UUID) ([]Earned, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Earned
	for _, e := range r.earned[id] {
		out = append(out, e)
	}
	return out, r.backfilled[id], nil
}

func (r *fakeRepo) Grant(_ context.Context, id uuid.UUID, units []Unit, source Source) ([]Unit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.earned[id] == nil {
		r.earned[id] = map[Unit]Earned{}
	}
	var out []Unit
	for _, u := range units {
		if _, ok := r.earned[id][u]; ok {
			continue
		}
		r.earned[id][u] = Earned{Unit: u, At: time.Now(), Source: source}
		out = append(out, u)
	}
	if source == SourceBackfill {
		r.backfilled[id] = true
	}
	return out, nil
}

func (r *fakeRepo) MarkSeen(_ context.Context, id uuid.UUID, ids []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for u, e := range r.earned[id] {
		if len(ids) == 0 || strings.Contains(","+strings.Join(ids, ",")+",", ","+u.ID+",") {
			e.Seen = true
			r.earned[id][u] = e
		}
	}
	return nil
}

func (r *fakeRepo) Rarity(context.Context) (Rarity, error) { return r.rarity, nil }

func (r *fakeRepo) RefreshRarity(context.Context, time.Time, time.Time) (bool, error) {
	return true, nil
}

func (r *fakeRepo) Unbackfilled(_ context.Context, limit int) ([]uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []uuid.UUID
	for id, done := range r.backfilled {
		if !done && len(out) < limit {
			out = append(out, id)
		}
	}
	return out, nil
}

func (r *fakeRepo) Timezone(context.Context, uuid.UUID) (string, error) { return r.tz, nil }

func (r *fakeRepo) ClaimPushSlot(_ context.Context, id uuid.UUID, kind string, d time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := id.String() + kind + d.Format(time.DateOnly)
	if r.slots[k] {
		return false, nil
	}
	r.slots[k] = true
	return true, nil
}

// user makes a user whose past is already evaluated, with counts.
func (r *fakeRepo) user(counts map[Counter]int64) uuid.UUID {
	id := uuid.New()
	r.counts[id] = counts
	r.backfilled[id] = true
	return id
}

type pushes struct {
	mu     sync.Mutex
	titles []string
}

func (p *pushes) AchievementUnlocked(_ context.Context, _ uuid.UUID, title, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.titles = append(p.titles, title)
}

type fakeViewer struct{ visible, blocked bool }

func (v fakeViewer) CanView(context.Context, uuid.UUID, uuid.UUID) (bool, bool, error) {
	return v.visible, v.blocked, nil
}

// noon on Thursday 2026-10-22 UTC.
var noon = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func newTestService(repo *fakeRepo, n Notifier, v Viewer) *Service {
	s := NewService(repo, n, v, nil, true)
	s.now = func() time.Time { return noon }
	return s
}

func TestCatalogueInvariants(t *testing.T) {
	seen := map[string]bool{}
	counts := map[Kind]int{}
	for _, d := range Catalogue {
		if seen[d.ID] {
			t.Errorf("duplicate id %q", d.ID)
		}
		seen[d.ID] = true
		counts[d.Kind]++
		if d.Title == "" || d.Icon == "" || d.Blurb == "" || d.Category == "" || d.Counter == "" {
			t.Errorf("%s is missing copy", d.ID)
		}
		if strings.ContainsAny(d.ID, " _:") || strings.ToLower(d.ID) != d.ID {
			t.Errorf("%s: ids are lower-case kebab", d.ID)
		}
		hasVerb := strings.Contains(d.Blurb, "%d")
		if d.Kind == KindTrack {
			if !hasVerb {
				t.Errorf("%s: a track's blurb names its next count", d.ID)
			}
			for i := 1; i < len(d.Tiers); i++ {
				if d.Tiers[i] <= d.Tiers[i-1] || d.Tiers[0] <= 0 {
					t.Errorf("%s: tiers must rise from above 0: %v", d.ID, d.Tiers)
				}
			}
		} else if hasVerb {
			t.Errorf("%s: only tracks format their blurb", d.ID)
		}
	}
	if counts[KindTrack] != 15 || counts[KindMoment] != 15 || counts[KindSecret] != 4 {
		t.Errorf("catalogue = %v, want 15 tracks, 15 moments, 4 secrets", counts)
	}
	if TotalUnits() != 79 {
		t.Errorf("total = %d, want 79", TotalUnits())
	}
	// Every counter a definition names is counted somewhere.
	counted := map[Counter]bool{CountNightVisits: true}
	for _, q := range counterSQL {
		counted[q.c] = true
	}
	for _, d := range Catalogue {
		if !counted[d.Counter] {
			t.Errorf("%s: counter %q has no query", d.ID, d.Counter)
		}
	}
}

func TestKeysAndLabels(t *testing.T) {
	cases := map[Unit]string{
		{ID: "cartographer", Tier: TierGold}: "Cartographer · Gold",
		{ID: "first-pin"}:                    "First Pin",
		{ID: "town-crier", Tier: TierBronze}: "Town Crier · Bronze",
	}
	for u, want := range cases {
		got, ok := Label(u.Key())
		if !ok || got != want {
			t.Errorf("Label(%q) = %q, %v; want %q", u.Key(), got, ok, want)
		}
	}
	if _, ok := Label("first-pin-gold"); ok {
		t.Error("a moment has no tiers")
	}
	if _, ok := Label("trip-finisher"); ok {
		t.Error("an old badge id is not an achievement")
	}
}

func TestDueGrantsEveryTierReachedOnce(t *testing.T) {
	counts := map[Counter]int64{CountCities: 16, CountRankings: 9}
	held := map[Unit]Earned{{ID: "cartographer", Tier: TierBronze}: {}}
	got := map[Unit]bool{}
	for _, u := range Due(counts, held) {
		got[u] = true
	}
	want := []Unit{{ID: "cartographer", Tier: TierSilver}, {ID: "first-pin"}}
	if len(got) != len(want) {
		t.Fatalf("due = %v, want %v", got, want)
	}
	for _, u := range want {
		if !got[u] {
			t.Errorf("missing %v", u)
		}
	}
}

func TestBackfillIsSilent(t *testing.T) {
	repo := newFakeRepo()
	p := &pushes{}
	s := newTestService(repo, p, nil)
	u := repo.user(map[Counter]int64{CountCities: 6, CountStops: 25})
	repo.backfilled[u] = false

	got, err := s.Evaluate(context.Background(), u)
	if err != nil || len(got) != 0 {
		t.Fatalf("backfill returned %v, %v; want nothing", got, err)
	}
	if len(p.titles) != 0 {
		t.Fatalf("backfill pushed %v", p.titles)
	}
	if !repo.backfilled[u] || len(repo.earned[u]) != 3 {
		t.Fatalf("backfill granted %v (backfilled %v)", repo.earned[u], repo.backfilled[u])
	}
	for _, e := range repo.earned[u] {
		if e.Source != SourceBackfill {
			t.Fatalf("%v granted as %s", e.Unit, e.Source)
		}
	}

	// Someone with nothing yet is still marked done.
	empty := repo.user(nil)
	repo.backfilled[empty] = false
	if _, err := s.Evaluate(context.Background(), empty); err != nil || !repo.backfilled[empty] {
		t.Fatalf("an empty past was not marked evaluated: %v", err)
	}

	list, err := s.List(context.Background(), u, u)
	if err != nil || list.GetUnseenBackfillCount() != 3 || list.GetEarnedCount() != 3 {
		t.Fatalf("list = earned %d unseen backfill %d, %v", list.GetEarnedCount(), list.GetUnseenBackfillCount(), err)
	}
	if err := s.MarkSeen(context.Background(), u, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(context.Background(), u, u); list.GetUnseenBackfillCount() != 0 {
		t.Fatalf("seen backfill still counted: %d", list.GetUnseenBackfillCount())
	}
}

func TestLiveUnlockPushesTheBestOnceADay(t *testing.T) {
	repo := newFakeRepo()
	p := &pushes{}
	s := newTestService(repo, p, nil)
	u := repo.user(map[Counter]int64{CountStops: 80, CountDays: 1})
	ctx := context.Background()

	got, err := s.Evaluate(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]achievementv1.AchievementTier{}
	for _, a := range got {
		ids[a.GetId()] = a.GetTier()
		if !a.GetIsNew() {
			t.Errorf("%s unlocked but not new", a.GetId())
		}
	}
	if len(got) != 2 || ids["trailhead"] != achievementv1.AchievementTier_ACHIEVEMENT_TIER_SILVER {
		t.Fatalf("unlocked %v; want trailhead at silver and day-one-done", ids)
	}
	if len(p.titles) != 1 || p.titles[0] != "Trailhead · Silver" {
		t.Fatalf("pushes = %v; want only the highest unlock", p.titles)
	}

	repo.counts[u][CountCities] = 1
	if got, _ := s.Evaluate(ctx, u); len(got) != 1 {
		t.Fatalf("second unlock = %v", got)
	}
	if len(p.titles) != 1 {
		t.Fatalf("a second achievement push the same day: %v", p.titles)
	}
	if got, _ := s.Evaluate(ctx, u); got != nil {
		t.Fatalf("nothing new still returned %v", got)
	}
}

func TestNoUnlockPushAtNight(t *testing.T) {
	repo := newFakeRepo()
	p := &pushes{}
	s := newTestService(repo, p, nil)
	s.now = func() time.Time { return time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC) }
	u := repo.user(map[Counter]int64{CountCities: 1})
	if got, _ := s.Evaluate(context.Background(), u); len(got) != 1 {
		t.Fatalf("the unlock still counts at night: %v", got)
	}
	if len(p.titles) != 0 {
		t.Fatalf("pushed at 23:00: %v", p.titles)
	}
	// Night took no slot: the next unlock in the morning can push.
	s.now = func() time.Time { return time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC) }
	repo.counts[u][CountTrips] = 1
	_, _ = s.Evaluate(context.Background(), u)
	if len(p.titles) != 1 {
		t.Fatalf("morning pushes = %v", p.titles)
	}
}

func TestListShowsProgressAndHidesSecrets(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := repo.user(map[Counter]int64{CountCities: 7, CountRankings: 4})
	list, err := s.List(context.Background(), u, u)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetAchievements()) != len(Catalogue) || list.GetTotal() != 79 {
		t.Fatalf("listed %d of %d", len(list.GetAchievements()), list.GetTotal())
	}
	byID := map[string]*achievementv1.Achievement{}
	for _, a := range list.GetAchievements() {
		byID[a.GetId()] = a
	}
	carto := byID["cartographer"]
	if carto.GetTier() != achievementv1.AchievementTier_ACHIEVEMENT_TIER_BRONZE || carto.GetProgress() != 7 ||
		carto.GetNextTarget() != 15 || carto.GetDescription() != "Visit 15 cities." || len(carto.GetSteps()) != 4 {
		t.Errorf("cartographer = %v", carto)
	}
	if taste := byID["taste-ledger"]; taste.GetProgress() != 4 || taste.GetNextTarget() != 10 || taste.GetEarnedAt() != nil {
		t.Errorf("taste-ledger = %v", taste)
	}
	if owl := byID["night-owl"]; !owl.GetHidden() || owl.GetTitle() != "" || owl.GetDescription() != "" {
		t.Errorf("an unearned secret shows: %v", owl)
	}
	if carto.RarityPct != nil {
		t.Error("rarity shown with no base")
	}

	repo.rarity = Rarity{Base: 200, Holders: map[Unit]int{{ID: "cartographer", Tier: TierBronze}: 30}}
	list, _ = s.List(context.Background(), u, u)
	if pct := list.GetAchievements()[0].RarityPct; pct == nil || *pct != 15 {
		t.Errorf("rarity = %v, want 15%%", pct)
	}
}

func TestListRespectsPrivacy(t *testing.T) {
	repo := newFakeRepo()
	owner := repo.user(map[Counter]int64{CountCities: 1})
	viewer := uuid.New()
	ctx := context.Background()

	if _, err := newTestService(repo, nil, fakeViewer{}).List(ctx, viewer, owner); !errors.Is(err, gamification.ErrPrivate) {
		t.Errorf("private account: err = %v", err)
	}
	if _, err := newTestService(repo, nil, fakeViewer{blocked: true}).List(ctx, viewer, owner); !errors.Is(err, gamification.ErrNotFound) {
		t.Errorf("blocked: err = %v", err)
	}
	s := newTestService(repo, &pushes{}, fakeViewer{visible: true})
	list, err := s.List(ctx, viewer, owner)
	if err != nil {
		t.Fatal(err)
	}
	if list.GetEarnedCount() != 0 || list.GetUnseenBackfillCount() != 0 {
		t.Errorf("someone else's list evaluated them: %v", list)
	}
	// The owner's own list evaluates first.
	list, _ = s.List(ctx, owner, owner)
	if list.GetEarnedCount() != 1 {
		t.Fatalf("own list earned = %d", list.GetEarnedCount())
	}
	list, _ = s.List(ctx, viewer, owner)
	for _, a := range list.GetAchievements() {
		if a.GetIsNew() {
			t.Errorf("%s is new to someone else", a.GetId())
		}
	}
}

func TestKillSwitch(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, nil, nil, nil, false)
	u := repo.user(map[Counter]int64{CountCities: 1})
	if got, err := s.Evaluate(context.Background(), u); got != nil || err != nil || len(repo.earned[u]) != 0 {
		t.Fatalf("off, evaluate granted %v, %v", repo.earned[u], err)
	}
	list, err := s.List(context.Background(), u, u)
	if err != nil || len(list.GetAchievements()) != 0 {
		t.Fatalf("off, list = %v, %v", list, err)
	}
	var nilSvc *Service
	nilSvc.Touch(context.Background(), u)
}

func TestBackfillBatch(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, &pushes{}, nil)
	for range 3 {
		u := repo.user(map[Counter]int64{CountCities: 1})
		repo.backfilled[u] = false
	}
	n, err := s.BackfillBatch(context.Background(), 2)
	if err != nil || n != 2 {
		t.Fatalf("batch = %d, %v", n, err)
	}
	left, _ := repo.Unbackfilled(context.Background(), 10)
	if len(left) != 1 {
		t.Fatalf("left = %d, want 1", len(left))
	}
}

type feedPosts struct {
	mu    sync.Mutex
	posts []string
	days  []time.Time
}

func (f *feedPosts) PostAchievement(_ context.Context, _ uuid.UUID, a *achievementv1.AchievementSummary, rank int, day time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, fmt.Sprintf("%s/%d/%d", a.GetId(), a.GetTier(), rank))
	f.days = append(f.days, day)
}

// The feed gets the best live unlock every time, night or day, and never a
// backfilled one.
func TestLiveUnlocksReachTheFeed(t *testing.T) {
	repo := newFakeRepo()
	f := &feedPosts{}
	s := newTestService(repo, &pushes{}, nil).WithFeed(f)
	ctx := context.Background()

	old := repo.user(map[Counter]int64{CountCities: 40})
	repo.backfilled[old] = false
	_, _ = s.Evaluate(ctx, old)
	if len(f.posts) != 0 {
		t.Fatalf("backfill reached the feed: %v", f.posts)
	}

	repo.tz = "Pacific/Auckland" // 01:00 on Friday there: quiet, but still posted
	u := repo.user(map[Counter]int64{CountStops: 80, CountDays: 1})
	_, _ = s.Evaluate(ctx, u)
	if len(f.posts) != 1 || f.posts[0] != "trailhead/2/3" {
		t.Fatalf("posts = %v; want trailhead at silver, rank 3", f.posts)
	}
	if want := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC); !f.days[0].Equal(want) {
		t.Fatalf("day = %s; want the user's local %s", f.days[0], want)
	}
}

func TestShowcaseIsNewestThenRarest(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := repo.user(nil)
	at := func(h int) time.Time { return noon.Add(-time.Duration(h) * time.Hour) }
	held := []Earned{
		{Unit: Unit{ID: "cartographer", Tier: TierBronze}, At: at(10)},
		{Unit: Unit{ID: "cartographer", Tier: TierSilver}, At: at(1)},
		{Unit: Unit{ID: "first-pin"}, At: at(9)},
		{Unit: Unit{ID: "night-owl"}, At: at(30)},
		{Unit: Unit{ID: "day-one-done"}, At: at(20)},
		{Unit: Unit{ID: "round-trip"}, At: at(21)},
		{Unit: Unit{ID: "kindred"}, At: at(22)},
		{Unit: Unit{ID: "first-light"}, At: at(23)},
		{Unit: Unit{ID: "retired-badge"}, At: at(0)},
	}
	repo.earned[u] = map[Unit]Earned{}
	for _, e := range held {
		repo.earned[u][e.Unit] = e
	}
	repo.rarity = Rarity{Base: 100, Holders: map[Unit]int{
		{ID: "night-owl"}: 2, {ID: "first-light"}: 5, {ID: "kindred"}: 40,
	}}

	top, earned, err := s.Showcase(context.Background(), u, 6)
	if err != nil {
		t.Fatal(err)
	}
	if earned != 8 {
		t.Errorf("earned = %d; want 8 units (the unknown id left out)", earned)
	}
	var got []string
	for _, a := range top {
		got = append(got, fmt.Sprintf("%s/%d", a.GetId(), a.GetTier()))
	}
	want := []string{"cartographer/2", "first-pin/0", "day-one-done/0", "round-trip/0", "night-owl/0", "first-light/0"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("showcase = %v; want %v", got, want)
	}
	if top[0].GetDescription() == "" || top[0].GetTitle() != "Cartographer" {
		t.Errorf("summary = %+v", top[0])
	}
}
