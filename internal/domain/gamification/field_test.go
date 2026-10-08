package gamification

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
)

func TestSeasonIDFollowsISOWeeks(t *testing.T) {
	cases := map[string]int{
		"2026-10-08": 202641,
		"2026-12-31": 202653,
		"2027-01-03": 202653, // Sunday: still the last week of 2026
		"2027-01-04": 202701,
		"2026-01-01": 202601,
	}
	for date, want := range cases {
		d, _ := time.Parse("2006-01-02", date)
		if got := SeasonID(d); got != want {
			t.Errorf("SeasonID(%s) = %d, want %d", date, got, want)
		}
		if start := SeasonStart(want); start.Weekday() != time.Monday || SeasonID(start) != want {
			t.Errorf("SeasonStart(%d) = %v", want, start)
		}
	}
	if got := SeasonShift(202701, -1); got != 202653 {
		t.Errorf("week before 202701 = %d", got)
	}
}

func TestSeasonOverWaitsForTheLastTimezone(t *testing.T) {
	// Season 202640 is Mon 2026-09-28 to Sun 2026-10-04.
	mondayNoonUTC := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if SeasonOver(202640, mondayNoonUTC) {
		t.Fatalf("UTC-12 is still in Sunday at Monday 12:00 UTC")
	}
	if !SeasonOver(202640, mondayNoonUTC.Add(3*time.Hour)) {
		t.Fatalf("the season should be over by Monday 15:00 UTC")
	}
}

func TestRankFor(t *testing.T) {
	th := Thresholds{0, 60, 200, 500, 1200}
	cases := []struct {
		score int64
		rank  FieldRank
		next  int64
	}{
		{0, RankScout, 60},
		{59, RankScout, 60},
		{60, RankWalker, 200},
		{499, RankGuide, 500},
		{500, RankLocal, 1200},
		{1200, RankKeeper, 0},
		{99999, RankKeeper, 0},
	}
	for _, c := range cases {
		if r, n := RankFor(c.score, th); r != c.rank || n != c.next {
			t.Errorf("RankFor(%d) = %v, %d; want %v, %d", c.score, r, n, c.rank, c.next)
		}
	}
}

func TestParseThresholds(t *testing.T) {
	if got, err := ParseThresholds("", DefaultCityThresholds); err != nil || got != DefaultCityThresholds {
		t.Fatalf("empty = %v, %v", got, err)
	}
	if got, err := ParseThresholds("0, 10, 20, 30, 40", DefaultCityThresholds); err != nil || got != (Thresholds{0, 10, 20, 30, 40}) {
		t.Fatalf("parsed = %v, %v", got, err)
	}
	for _, bad := range []string{"0,10,20", "5,10,20,30,40", "0,10,10,30,40", "0,a,20,30,40"} {
		if got, err := ParseThresholds(bad, DefaultCityThresholds); err == nil || got != DefaultCityThresholds {
			t.Errorf("%q = %v, %v; want the default and an error", bad, got, err)
		}
	}
}

func TestNoteIsOriginal(t *testing.T) {
	desc := "A covered market from 1940 selling exotic fruit, fresh fish and flowers, with tiled facades by João Rodrigues."
	cases := map[string]bool{
		"Too short to count.": false,
		// 39 runes exactly.
		strings.Repeat("a", 39): false,
		desc:                    false,
		"Covered market from 1940 selling exotic fruit, fresh fish & flowers — loved it":        false,
		"Went at 8am before the tour buses. The passion fruit lady on the left gives tastings.": true,
	}
	for note, want := range cases {
		if got := NoteIsOriginal(note, []string{"", desc}); got != want {
			t.Errorf("NoteIsOriginal(%q) = %v, want %v", note, got, want)
		}
	}
}

func TestSlugFoldsAccents(t *testing.T) {
	if a, b := Slug("São Bento"), Slug("sao  bento!"); a != b || a != "sao-bento" {
		t.Fatalf("slugs = %q, %q", a, b)
	}
}

func TestSavingAPlacePaysOnceAndIsCapped(t *testing.T) {
	repo := newFakeRepo()
	funchal := uuid.New()
	repo.cities["funchal"] = funchal
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	ctx := context.Background()
	mercado := SavedItem{ItemID: "mercado", ContentType: "poi", Name: "Mercado dos Lavradores", CityName: "Funchal"}

	if got := s.PlaceSaved(ctx, u, mercado); got != Rules[KindPlaceSaved].Points {
		t.Fatalf("first save = %d", got)
	}
	// Unsave and save again, twice: the key is the place, so nothing more.
	for i := 0; i < 2; i++ {
		if got := s.PlaceSaved(ctx, u, mercado); got != 0 {
			t.Fatalf("re-save %d paid %d", i, got)
		}
	}
	if len(repo.events) != 1 || repo.events[0].CityID == nil || *repo.events[0].CityID != funchal {
		t.Fatalf("events = %+v; want one row on Funchal", repo.events)
	}
	if !strings.Contains(repo.events[0].Label, "Mercado dos Lavradores") {
		t.Fatalf("label = %q", repo.events[0].Label)
	}

	limit := Rules[KindPlaceSaved].DailyCap
	paid := 1
	for i := 0; i < limit+3; i++ {
		if s.PlaceSaved(ctx, u, SavedItem{ItemID: uuid.NewString(), ContentType: "poi"}) > 0 {
			paid++
		}
	}
	if paid != limit {
		t.Fatalf("paid %d saves in a day, want the cap of %d", paid, limit)
	}
	if got := s.PlaceSaved(ctx, u, SavedItem{ItemID: "trip", ContentType: "itinerary"}); got != 0 {
		t.Fatalf("a saved itinerary is not a place, paid %d", got)
	}
}

func TestNoteWrittenNeedsOwnWords(t *testing.T) {
	repo := newFakeRepo()
	desc := "A covered market from 1940 selling exotic fruit, fresh fish and flowers."
	repo.notes["poi:mercado"] = []string{desc, ""}
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	ctx := context.Background()
	it := SavedItem{ItemID: "mercado", ContentType: "poi", Name: "Mercado"}

	if p, counts := s.NoteWritten(ctx, u, it, desc); p != 0 || counts {
		t.Fatalf("pasted description = %d, %v", p, counts)
	}
	own := "Went at 8am before the tour buses; the passion fruit stall on the left gives tastings."
	if p, counts := s.NoteWritten(ctx, u, it, own); p != Rules[KindPlaceNote].Points || !counts {
		t.Fatalf("own note = %d, %v", p, counts)
	}
	if p, counts := s.NoteWritten(ctx, u, it, own+" Again."); p != 0 || !counts {
		t.Fatalf("a second note on the same place = %d, %v; counts but pays once", p, counts)
	}
	if p, _ := s.NoteWritten(ctx, u, SavedItem{ItemID: "unsaved", ContentType: "poi"}, own); p != 0 {
		t.Fatalf("a note on a place not saved paid %d", p)
	}
}

type fakeHoods map[[2]float64]string

func (f fakeHoods) Neighborhood(_ context.Context, lat, lon float64) (string, error) {
	return f[[2]float64{lat, lon}], nil
}

func TestMarkStopPaysOnceAndFinishesDaysAndTrips(t *testing.T) {
	repo := newFakeRepo()
	rome := uuid.New()
	repo.places["pantheon"] = Place{Found: true, Name: "Pantheon", HasLocation: true, Lat: 41.8986, Lon: 12.4769, CityID: &rome}
	s := newTestService(repo, nil, nil).WithNeighborhoods(fakeHoods{{41.8986, 12.4769}: "Pigna"})
	u := uuid.New()
	trip := newTrip(repo, u, &rome, 2, 1)
	trip.Days[0].Stops[0].POIID = "pantheon"
	repo.trips[trip.ID].state.Days[0].Stops[0].POIID = "pantheon"
	tid := trip.ID.String()
	a, b := trip.Days[0].Stops[0].ID.String(), trip.Days[0].Stops[1].ID.String()
	c := trip.Days[1].Stops[0].ID.String()
	ctx := context.Background()

	if _, err := s.MarkStop(ctx, uuid.New(), tid, a, StopDone, "UTC"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's trip: %v", err)
	}
	if _, err := s.MarkStop(ctx, u, tid, uuid.NewString(), StopDone, "UTC"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown stop: %v", err)
	}

	first, err := s.MarkStop(ctx, u, tid, a, StopDone, "UTC")
	want := Rules[KindStopDone].Points + Rules[KindNewCity].Points + Rules[KindNeighborhood].Points
	if err != nil || first.Points != want || first.DayFinished {
		t.Fatalf("first stop = %+v, %v; want %d (stop, first city, first neighborhood)", first, err, want)
	}
	if p := repo.places["pantheon"]; p.Neighborhood != "Pigna" || !p.NeighborhoodChecked {
		t.Fatalf("neighborhood not stored: %+v", p)
	}
	// Reopen and mark done again: never repays.
	if _, err := s.MarkStop(ctx, u, tid, a, StopOpen, "UTC"); err != nil {
		t.Fatal(err)
	}
	again, _ := s.MarkStop(ctx, u, tid, a, StopDone, "UTC")
	if again.Points != 0 {
		t.Fatalf("done → open → done repaid %d", again.Points)
	}
	// Skipping the other stop finishes the day.
	day, err := s.MarkStop(ctx, u, tid, b, StopSkipped, "UTC")
	if err != nil || !day.DayFinished || day.Points != Rules[KindTripDayCompleted].Points || day.TripFinished {
		t.Fatalf("finishing day one = %+v, %v", day, err)
	}
	// The last day's only stop finishes the trip.
	last, err := s.MarkStop(ctx, u, tid, c, StopDone, "UTC")
	want = Rules[KindStopDone].Points + Rules[KindTripDayCompleted].Points + Rules[KindTripCompleted].Points
	if err != nil || !last.TripFinished || last.Points != want {
		t.Fatalf("last stop = %+v, %v; want %d", last, err, want)
	}
	if last.WeekScore != repo.season(u, uuid.Nil, SeasonID(noon)).Score || last.WeekScore == 0 {
		t.Fatalf("week score = %d", last.WeekScore)
	}
	if got := repo.season(u, rome, SeasonID(noon)); got.Score != last.WeekScore || got.DaysFinished != 2 {
		t.Fatalf("Rome's week = %+v; finishing the trip moves the city board", got)
	}
}

func TestADaySkippedWholeIsNotFinished(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	trip := newTrip(repo, u, nil, 2)
	for _, st := range trip.Days[0].Stops {
		res, err := s.MarkStop(context.Background(), u, trip.ID.String(), st.ID.String(), StopSkipped, "UTC")
		if err != nil || res.DayFinished || res.Points != 0 {
			t.Fatalf("skip = %+v, %v", res, err)
		}
	}
}

func TestLegacyCompleteTripDayDefersToMarks(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	trip := newTrip(repo, u, nil, 2, 1)
	ctx := context.Background()
	if _, err := s.MarkStop(ctx, u, trip.ID.String(), trip.Days[0].Stops[0].ID.String(), StopSkipped, "UTC"); err != nil {
		t.Fatal(err)
	}
	// An older app claims 2 stops done, but the marks say the day is not.
	res, err := s.CompleteTripDay(ctx, u, trip.ID.String(), trip.Days[0].ID.String(), 2, "UTC")
	if err != nil || res.Points != 0 {
		t.Fatalf("legacy completion over marks = %+v, %v", res, err)
	}
}

// boardSetup: me, a friend and strangers scored in Funchal this week.
func boardSetup(t *testing.T, strangers int) (*fakeRepo, *Service, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	repo := newFakeRepo()
	funchal := uuid.New()
	repo.cities["funchal"] = funchal
	me, friend := uuid.New(), uuid.New()
	repo.defaultCity[me] = funchal
	graph := fakeGraph{friends: map[uuid.UUID][]uuid.UUID{me: {friend}, friend: {me}}}
	s := newTestService(repo, graph, nil)
	ctx := context.Background()
	award := func(u uuid.UUID, n int) {
		for i := 0; i < n; i++ {
			if _, err := s.Award(ctx, Award{UserID: u, Kind: KindStopDone, RefKey: "stop:" + uuid.NewString(), CityID: &funchal}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Strangers score 3..strangers+2 stops; me 1 stop; friend 2.
	for i := 0; i < strangers; i++ {
		award(uuid.New(), i+3)
	}
	award(me, 1)
	award(friend, 2)
	return repo, s, me, friend, funchal
}

func TestCityBoardShowsTopTenTheViewerAndTheRowAbove(t *testing.T) {
	_, s, me, friend, funchal := boardSetup(t, 12)
	b, err := s.Board(context.Background(), me, BoardRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Scope != ScopeCityWeek || b.CityID == nil || *b.CityID != funchal || b.CityName != "funchal" {
		t.Fatalf("board = %+v; the default is this week in the viewer's city", b)
	}
	if len(b.Top) != 10 || b.Scored != 14 || b.TooFew {
		t.Fatalf("top %d of %d, tooFew %v", len(b.Top), b.Scored, b.TooFew)
	}
	if b.Me == nil || !b.Me.IsMe || b.Me.Position != 14 || b.Me.User == nil {
		t.Fatalf("me = %+v", b.Me)
	}
	if b.Above == nil || b.Above.Position != 13 || b.Above.User == nil || b.Above.User.GetId() != friend.String() {
		t.Fatalf("above = %+v; want the friend, with their card", b.Above)
	}
	for _, r := range b.Top {
		if r.User != nil {
			t.Fatalf("a stranger's card leaked: %+v", r)
		}
		if r.DisplayName == "" {
			t.Fatalf("every row has a display name: %+v", r)
		}
	}
}

func TestCityBoardHonoursOptOutAndSaysWhenThin(t *testing.T) {
	repo, s, me, friend, _ := boardSetup(t, 2)
	repo.cityHidden[me] = true
	ctx := context.Background()
	mine, err := s.Board(ctx, me, BoardRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !mine.MeHidden || !mine.TooFew || mine.Scored != 4 {
		t.Fatalf("my board = %+v; I still see myself, flagged hidden", mine)
	}
	repo.defaultCity[friend] = repo.cities["funchal"]
	theirs, err := s.Board(ctx, friend, BoardRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range theirs.Top {
		if r.User != nil && r.User.GetId() == me.String() {
			t.Fatalf("an opted-out traveller showed on someone else's city board")
		}
	}
	if theirs.Scored != 3 {
		t.Fatalf("scored = %d, want 3 without me", theirs.Scored)
	}
}

func TestBoardWithoutACityIsEmptyNotAnError(t *testing.T) {
	s := newTestService(newFakeRepo(), fakeGraph{}, nil)
	b, err := s.Board(context.Background(), uuid.New(), BoardRequest{})
	if err != nil || b.CityID != nil || len(b.Top) != 0 || b.FriendsAvailable {
		t.Fatalf("board = %+v, %v", b, err)
	}
}

func TestFriendsBoardNeedsAFriendAndHonoursVisibility(t *testing.T) {
	repo, s, me, friend, _ := boardSetup(t, 3)
	ctx := context.Background()
	b, err := s.Board(ctx, me, BoardRequest{Scope: ScopeFriendsWeek})
	if err != nil || !b.FriendsAvailable || b.Scored != 2 || len(b.Top) != 2 {
		t.Fatalf("friends board = %+v, %v; want me and my friend only", b, err)
	}
	repo.hidden[friend] = true
	if b, _ = s.Board(ctx, me, BoardRequest{Scope: ScopeFriendsWeek}); b.Scored != 1 {
		t.Fatalf("a friend who hid themselves still shows: %+v", b)
	}
	alone := newTestService(newFakeRepo(), fakeGraph{}, nil)
	if b, _ := alone.Board(ctx, uuid.New(), BoardRequest{Scope: ScopeFriendsWeek}); b.FriendsAvailable || len(b.Top) != 0 {
		t.Fatalf("no friends: %+v", b)
	}
}

func TestPersonalBoardComparesWeeks(t *testing.T) {
	_, s, me, _, _ := boardSetup(t, 0)
	b, err := s.Board(context.Background(), me, BoardRequest{Scope: ScopePersonal})
	if err != nil || b.Personal == nil || b.Personal.This.Score != int64(Rules[KindStopDone].Points) || b.Personal.Last.Score != 0 {
		t.Fatalf("personal = %+v, %v", b.Personal, err)
	}
}

type fakePlans string

func (p fakePlans) EffectivePlan(context.Context, uuid.UUID) (string, error) { return string(p), nil }

func TestPastWeeksFollowPlanGating(t *testing.T) {
	_, s, me, _, _ := boardSetup(t, 0)
	s.WithPlans(fakePlans("free"))
	ctx := context.Background()
	subscription.SetGating(false)
	if b, _ := s.Board(ctx, me, BoardRequest{SeasonOffset: -1}); b.HistoryLocked {
		t.Fatalf("gating off: last week is open to everyone")
	}
	subscription.SetGating(true)
	defer subscription.SetGating(false)
	if b, _ := s.Board(ctx, me, BoardRequest{SeasonOffset: -1}); !b.HistoryLocked || len(b.Top) != 0 {
		t.Fatalf("gating on, free plan: %+v", b)
	}
	if b, _ := s.Board(ctx, me, BoardRequest{}); b.HistoryLocked {
		t.Fatalf("this week is never locked")
	}
}

func TestProfileRanksPerCity(t *testing.T) {
	_, s, me, _, funchal := boardSetup(t, 0)
	ctx := context.Background()
	// Push me to Walker in Funchal (30) but not overall (60).
	for i := 0; i < 3; i++ {
		if _, err := s.Award(ctx, Award{UserID: me, Kind: KindStopDone, RefKey: "stop:" + uuid.NewString(), CityID: &funchal}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.Profile(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	if p.Lifetime != 32 || p.Rank != RankScout || p.Next != 60 || p.Week != 32 {
		t.Fatalf("profile = %+v", p)
	}
	if len(p.Cities) != 1 || p.Cities[0].Rank != RankWalker || p.Cities[0].Next != 100 {
		t.Fatalf("cities = %+v; want Walker in Funchal", p.Cities)
	}
}

func TestPayKeepsPaysOnce(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	repo.saved = []Keep{{UserID: u, ContentType: "poi", ItemID: "mercado", Name: "Mercado dos Lavradores"}}
	ctx := context.Background()
	if n, err := s.PayKeeps(ctx, 10); err != nil || n != 1 {
		t.Fatalf("first run paid %d, %v", n, err)
	}
	if n, _ := s.PayKeeps(ctx, 10); n != 0 {
		t.Fatalf("second run paid %d", n)
	}
	if repo.events[0].Label != "Kept Mercado dos Lavradores" {
		t.Fatalf("label = %q", repo.events[0].Label)
	}
}

func TestCloseSeasonsOnlyClosesFinishedWeeks(t *testing.T) {
	repo := newFakeRepo()
	s := newTestService(repo, nil, nil)
	u := uuid.New()
	repo.events = []Event{
		{UserID: u, SeasonID: 202639, FieldPoints: 8},
		{UserID: u, SeasonID: 202640, FieldPoints: 8},
		{UserID: u, SeasonID: 202641, FieldPoints: 8},
	}
	// Monday 2026-10-05 10:00 UTC: 202640 (ended Sunday) still has a time
	// zone in it; 202639 is long over; 202641 has not started.
	s.now = func() time.Time { return time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC) }
	if n, err := s.CloseSeasons(context.Background()); err != nil || n != 1 || !repo.closed[202639] || repo.closed[202640] {
		t.Fatalf("closed %d (%v): %v", n, err, repo.closed)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }
	if n, _ := s.CloseSeasons(context.Background()); n != 1 || !repo.closed[202640] {
		t.Fatalf("second pass closed %d: %v", n, repo.closed)
	}
}

func TestBackfillNeighborhoods(t *testing.T) {
	repo := newFakeRepo()
	repo.places["a"] = Place{Found: true, HasLocation: true, Lat: 1, Lon: 2}
	repo.places["b"] = Place{Found: true, HasLocation: true, Lat: 3, Lon: 4}
	s := newTestService(repo, nil, nil).WithNeighborhoods(fakeHoods{{1, 2}: "Alfama"})
	n, err := s.BackfillNeighborhoods(context.Background(), 10, 0)
	if err != nil || n != 2 {
		t.Fatalf("looked up %d, %v", n, err)
	}
	if repo.places["a"].Neighborhood != "Alfama" || !repo.places["b"].NeighborhoodChecked || repo.places["b"].Neighborhood != "" {
		t.Fatalf("places = %+v", repo.places)
	}
}
