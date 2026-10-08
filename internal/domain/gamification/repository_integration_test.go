//go:build integration

package gamification

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, username) VALUES ($1, $2, $3)`,
		id, id.String()+"@example.com", "u"+id.String()[:8]); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedCity(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO cities (name, country) VALUES ($1, $2) RETURNING id`, name, "Test "+uuid.NewString()[:8]).Scan(&id); err != nil {
		t.Fatalf("seed city: %v", err)
	}
	return id
}

func seedPOI(t *testing.T, pool *pgxpool.Pool, city uuid.UUID, name string, lat, lon float64) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO points_of_interest (name, location, city_id, description)
		VALUES ($1, ST_SetSRID(ST_MakePoint($3, $2), 4326), $4, 'A covered market from 1940.') RETURNING id`,
		name, lat, lon, city).Scan(&id); err != nil {
		t.Fatalf("seed poi: %v", err)
	}
	return id.String()
}

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func ev(u uuid.UUID, k Kind, ref string, pts int, date time.Time, city *uuid.UUID) Event {
	return Event{UserID: u, Kind: k, RefKey: ref, Points: pts, FieldPoints: pts, Label: ref, LocalDate: date, SeasonID: SeasonID(date), CityID: city}
}

func TestRepositoryAwardsOnceCapsAndKeepsAggregates(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u := seedUser(t, pool)
	funchal := seedCity(t, pool, "Funchal")

	e := ev(u, KindStopDone, "stop:a", 8, day(1), &funchal)
	ok, totals, err := repo.Insert(ctx, e, 0)
	if err != nil || !ok || totals.TotalPoints != 8 {
		t.Fatalf("first insert = %v, %+v, %v", ok, totals, err)
	}
	ok, totals, err = repo.Insert(ctx, e, 0)
	if err != nil || ok || totals.TotalPoints != 8 {
		t.Fatalf("repeat insert = %v, %+v, %v; want nothing new", ok, totals, err)
	}
	for i := 0; i < 3; i++ {
		inserted, _, err := repo.Insert(ctx, ev(u, KindPlaceSaved, "save:poi:"+uuid.NewString(), 2, day(1), nil), 2)
		if err != nil {
			t.Fatal(err)
		}
		if want := i < 2; inserted != want {
			t.Fatalf("save %d inserted = %v, want %v (cap 2)", i, inserted, want)
		}
	}
	if _, _, err := repo.Insert(ctx, ev(u, KindTripDayCompleted, "tripday:x", 15, day(1), &funchal), 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Insert(ctx, ev(u, KindPlaceKept, "kept:poi:y", 5, day(2), &funchal), 0); err != nil {
		t.Fatal(err)
	}

	stored, err := repo.Totals(ctx, u)
	if err != nil || stored.TotalPoints != 8+4+15+5 {
		t.Fatalf("lifetime = %+v, %v; want 32", stored, err)
	}
	week := SeasonID(day(1))
	city, err := repo.SeasonScores(ctx, u, funchal, []int{week})
	if err != nil || city[week] != (SeasonScore{Score: 28, PlacesKept: 1, DaysFinished: 1}) {
		t.Fatalf("Funchal week = %+v, %v", city, err)
	}
	overall, err := repo.SeasonScores(ctx, u, uuid.Nil, []int{week})
	if err != nil || overall[week].Score != 32 {
		t.Fatalf("overall week = %+v, %v; the city-less saves count overall only", overall, err)
	}
	cities, err := repo.CityTotals(ctx, u)
	if err != nil || len(cities) != 1 || cities[0].Score != 28 || cities[0].Name != "Funchal" {
		t.Fatalf("city totals = %+v, %v", cities, err)
	}
	life, err := repo.Lifetime(ctx, u)
	if err != nil || life.PlacesKept != 1 || life.DaysFinished != 1 {
		t.Fatalf("lifetime counts = %+v, %v", life, err)
	}
	all, err := repo.History(ctx, u, 10, time.Time{}, false)
	if err != nil || len(all) != 5 || all[0].CityName != "Funchal" || all[0].SeasonID != week {
		t.Fatalf("history = %d rows (%+v), %v", len(all), all, err)
	}
}

func TestRepositoryCapHoldsUnderConcurrentAwards(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u := seedUser(t, pool)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		paid int
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, err := repo.Insert(ctx, ev(u, KindPlaceSaved, "save:poi:"+uuid.NewString(), 2, day(3), nil), 10)
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				paid++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if paid != 10 {
		t.Fatalf("paid %d concurrent saves, want the cap of 10", paid)
	}
}

func TestRepositoryBoardIsScopedAndHonoursOptOutAndBlocks(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	city := seedCity(t, pool, "Board City")
	week := SeasonID(day(5))

	var users []uuid.UUID
	for i := 0; i < 14; i++ {
		u := seedUser(t, pool)
		users = append(users, u)
		// users[0] scores 14*8, users[13] scores 8.
		for j := 0; j < 14-i; j++ {
			if _, _, err := repo.Insert(ctx, ev(u, KindStopDone, "stop:"+uuid.NewString(), 8, day(5), &city), 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	viewer := users[12]
	page, err := repo.Board(ctx, BoardQuery{CityID: city, SeasonID: week, Metric: FieldOverall, Viewer: viewer})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Top) != 10 || page.Scored != 14 || page.Top[0].UserID != users[0] || page.Top[0].Position != 1 {
		t.Fatalf("top = %+v, scored %d", page.Top, page.Scored)
	}
	if page.Me == nil || page.Me.UserID != viewer || page.Me.Position != 13 {
		t.Fatalf("me = %+v", page.Me)
	}
	if page.Above == nil || page.Above.UserID != users[11] || page.Above.Position != 12 {
		t.Fatalf("above = %+v", page.Above)
	}

	// users[0] opts out of city boards and users[1] blocks the viewer.
	if _, err := pool.Exec(ctx, `INSERT INTO notification_settings (user_id, city_board_visible) VALUES ($1, FALSE)`, users[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, users[1], viewer); err != nil {
		t.Fatal(err)
	}
	page, err = repo.Board(ctx, BoardQuery{CityID: city, SeasonID: week, Metric: FieldOverall, Viewer: viewer})
	if err != nil || page.Scored != 12 {
		t.Fatalf("scored = %d, %v; want the opted-out and the blocker gone", page.Scored, err)
	}
	for _, r := range page.Top {
		if r.UserID == users[0] || r.UserID == users[1] {
			t.Fatalf("hidden user on the board: %+v", r)
		}
	}
	self, err := repo.Board(ctx, BoardQuery{CityID: city, SeasonID: week, Metric: FieldOverall, Viewer: users[0]})
	if err != nil || self.Top[0].UserID != users[0] {
		t.Fatalf("an opted-out traveller still sees themselves: %+v, %v", self.Top, err)
	}
	if visible, _ := repo.CityBoardVisible(ctx, users[0]); visible {
		t.Fatalf("switch reads on")
	}

	// The friends board is just the ids given, on the overall rows.
	friends, err := repo.Board(ctx, BoardQuery{CityID: uuid.Nil, SeasonID: week, Metric: FieldOverall, Viewer: viewer, Among: []uuid.UUID{viewer, users[13]}})
	if err != nil || friends.Scored != 2 || friends.Top[0].UserID != viewer {
		t.Fatalf("friends = %+v, %v", friends, err)
	}
	kept, err := repo.Board(ctx, BoardQuery{CityID: city, SeasonID: week, Metric: FieldPlacesKept, Viewer: viewer})
	if err != nil || kept.Scored != 0 {
		t.Fatalf("nobody kept a place: %+v, %v", kept, err)
	}
	lifetime, err := repo.LifetimeScores(ctx, city, []uuid.UUID{users[0], viewer})
	if err != nil || lifetime[users[0]] != 14*8 || lifetime[viewer] != 2*8 {
		t.Fatalf("lifetime = %v, %v", lifetime, err)
	}
}

func TestRepositoryClosesASeasonOnce(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	city := seedCity(t, pool, "Closing City")
	a, b := seedUser(t, pool), seedUser(t, pool)
	old := day(1).AddDate(-1, 0, 0) // a week long over
	season := SeasonID(old)
	for _, e := range []Event{ev(a, KindStopDone, "stop:1", 8, old, &city), ev(b, KindStopDone, "stop:2", 8, old, &city), ev(b, KindStopDone, "stop:3", 8, old, &city)} {
		if _, _, err := repo.Insert(ctx, e, 0); err != nil {
			t.Fatal(err)
		}
	}
	open, err := repo.OpenSeasons(ctx, SeasonID(day(8)))
	if err != nil || !containsInt(open, season) {
		t.Fatalf("open = %v, %v", open, err)
	}
	for i := 0; i < 2; i++ {
		if err := repo.CloseSeason(ctx, season); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
	if closed, _ := repo.SeasonClosed(ctx, season); !closed {
		t.Fatalf("not closed")
	}
	page, err := repo.Board(ctx, BoardQuery{CityID: city, SeasonID: season, Metric: FieldOverall, Viewer: a, Snapshot: true})
	if err != nil || len(page.Top) != 2 || page.Top[0].UserID != b || page.Top[1].Position != 2 {
		t.Fatalf("snapshot board = %+v, %v", page, err)
	}
	if open, _ = repo.OpenSeasons(ctx, SeasonID(day(8))); containsInt(open, season) {
		t.Fatalf("a closed season is still open: %v", open)
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestRepositoryTripMarksAndDays(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	owner, other := seedUser(t, pool), seedUser(t, pool)
	city := seedCity(t, pool, "Rome")

	var tripID, d1, d2, s1, s2 uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO trips (user_id, city_id, city_name, title) VALUES ($1, $2, 'Rome', 'Rome on foot') RETURNING id`, owner, city).Scan(&tripID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO trip_days (trip_id, day_number) VALUES ($1, 1) RETURNING id`, tripID).Scan(&d1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO trip_days (trip_id, day_number) VALUES ($1, 2) RETURNING id`, tripID).Scan(&d2); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO trip_stops (day_id, name, order_index, poi_id) VALUES ($1, 'Pantheon', 0, 'p1') RETURNING id`, d1).Scan(&s1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO trip_stops (day_id, name, order_index) VALUES ($1, 'Trevi', 1) RETURNING id`, d1).Scan(&s2); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.Trip(ctx, other, tripID); err != ErrNotFound {
		t.Fatalf("someone else's trip: %v", err)
	}
	if err := repo.SetMark(ctx, tripID, s1, StopDone); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetMark(ctx, tripID, s2, StopSkipped); err != nil {
		t.Fatal(err)
	}
	trip, err := repo.Trip(ctx, owner, tripID)
	if err != nil {
		t.Fatal(err)
	}
	if trip.Title != "Rome on foot" || trip.CityID == nil || *trip.CityID != city || len(trip.Days) != 2 {
		t.Fatalf("trip = %+v", trip)
	}
	if got := trip.Days[0]; len(got.Stops) != 2 || got.Stops[0].Status != StopDone || got.Stops[1].Status != StopSkipped || got.Stops[0].POIID != "p1" || !got.Finished() {
		t.Fatalf("day 1 = %+v", got)
	}
	if len(trip.Days[1].Stops) != 0 {
		t.Fatalf("day 2 has no stops: %+v", trip.Days[1])
	}
	if err := repo.SetMark(ctx, tripID, s2, StopOpen); err != nil {
		t.Fatal(err)
	}
	if trip, _ = repo.Trip(ctx, owner, tripID); trip.Days[0].Stops[1].Status != StopOpen {
		t.Fatalf("reopened stop = %+v", trip.Days[0].Stops[1])
	}
	newly, err := repo.CompleteDay(ctx, d1)
	if err != nil || !newly {
		t.Fatalf("complete = %v, %v", newly, err)
	}
	if newly, _ = repo.CompleteDay(ctx, d1); newly {
		t.Fatalf("completing twice reports newly")
	}
	if trip, _ = repo.Trip(ctx, owner, tripID); !trip.Done() {
		t.Fatalf("the only day with stops is finished, so the trip is: %+v", trip)
	}
}

func TestRepositoryPlacesCitiesAndNotes(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u := seedUser(t, pool)
	city := seedCity(t, pool, "Funchal Notes")
	poi := seedPOI(t, pool, city, "Mercado dos Lavradores", 32.6496, -16.9086)

	p, err := repo.Place(ctx, poi)
	if err != nil || !p.Found || !p.HasLocation || p.CityID == nil || *p.CityID != city || p.CityName != "Funchal Notes" || p.NeighborhoodChecked {
		t.Fatalf("place = %+v, %v", p, err)
	}
	if p.Lat < 32.6 || p.Lat > 32.7 || p.Lon > -16.9 {
		t.Fatalf("lat/lon swapped: %+v", p)
	}
	refs, err := repo.PlacesWithoutNeighborhood(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		if r.ID == poi {
			t.Fatalf("a place on no trip and never visited is not looked up")
		}
	}
	if err := repo.SetNeighborhood(ctx, poi, "São Pedro"); err != nil {
		t.Fatal(err)
	}
	if p, _ = repo.Place(ctx, poi); p.Neighborhood != "São Pedro" || !p.NeighborhoodChecked {
		t.Fatalf("after lookup = %+v", p)
	}
	if missing, _ := repo.Place(ctx, uuid.NewString()); missing.Found {
		t.Fatalf("unknown place found")
	}

	if id, err := repo.CityByName(ctx, "funchal notes"); err != nil || id == nil || *id != city {
		t.Fatalf("city by name = %v, %v", id, err)
	}
	if name, _ := repo.CityName(ctx, city); name != "Funchal Notes" {
		t.Fatalf("city name = %q", name)
	}

	if _, err := repo.NoteSources(ctx, u, "poi", poi); err != ErrNotFound {
		t.Fatalf("unsaved item: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_favorites (user_id, item_id, item_name, content_type, description)
		VALUES ($1, $2, 'Mercado', 'poi', 'Saved blurb.')`, u, poi); err != nil {
		t.Fatal(err)
	}
	src, err := repo.NoteSources(ctx, u, "poi", poi)
	if err != nil || len(src) != 2 || src[0] != "Saved blurb." || !strings.Contains(src[1], "covered market") {
		t.Fatalf("sources = %v, %v", src, err)
	}
}

func TestRepositoryDefaultCity(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u := seedUser(t, pool)
	lisbon, porto := seedCity(t, pool, "Lisbon Default"), seedCity(t, pool, "Porto Default")

	if id, _, err := repo.DefaultCity(ctx, u, day(8)); err != nil || id != nil {
		t.Fatalf("no trips or searches = %v, %v", id, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO trips (user_id, city_id, title) VALUES ($1, $2, 'Later')`, u, porto); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trips (user_id, city_id, title, start_date, end_date, updated_at)
		VALUES ($1, $2, 'Now', '2026-10-07', '2026-10-10', NOW() - INTERVAL '1 day')`, u, lisbon); err != nil {
		t.Fatal(err)
	}
	id, name, err := repo.DefaultCity(ctx, u, day(8))
	if err != nil || id == nil || *id != lisbon || name != "Lisbon Default" {
		t.Fatalf("default = %v %q, %v; the trip covering today wins", id, name, err)
	}
	if id, _, _ = repo.DefaultCity(ctx, u, day(20)); id == nil || *id != porto {
		t.Fatalf("default after the trip = %v; then the latest trip", id)
	}
}

func TestRepositoryDueKeeps(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u := seedUser(t, pool)
	city := seedCity(t, pool, "Keep City")
	mine := func() []Keep {
		all, err := repo.DueKeeps(ctx, 10000)
		if err != nil {
			t.Fatal(err)
		}
		var out []Keep
		for _, k := range all {
			if k.UserID == u {
				out = append(out, k)
			}
		}
		return out
	}

	// Saved 8 days ago through the field score; another bookmark from before
	// it, never scored; and one saved yesterday.
	for _, f := range []struct {
		item   string
		age    string
		scored bool
	}{{"old-scored", "8 days", true}, {"old-unscored", "30 days", false}, {"fresh", "1 day", true}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_favorites (user_id, item_id, item_name, content_type, added_at)
			VALUES ($1, $2, $2, 'poi', NOW() - $3::interval)`, u, f.item, f.age); err != nil {
			t.Fatal(err)
		}
		if f.scored {
			if _, _, err := repo.Insert(ctx, ev(u, KindPlaceSaved, "save:poi:"+f.item, 2, day(1), &city), 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	due := mine()
	if len(due) != 1 || due[0].ItemID != "old-scored" || due[0].CityID == nil || *due[0].CityID != city {
		t.Fatalf("due = %+v", due)
	}
	if _, _, err := repo.Insert(ctx, ev(u, KindPlaceKept, "kept:poi:old-scored", 5, day(9), &city), 0); err != nil {
		t.Fatal(err)
	}
	if due = mine(); len(due) != 0 {
		t.Fatalf("paid keep still due: %+v", due)
	}
}

// The 0116 rescore, run over rows the old economy wrote: retired kinds count
// 0, re-valued kinds count their new value, and lifetime totals follow.
func TestMigrationRescoresTheOldLedger(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	ctx := context.Background()
	up, down := migrationHalves(t)
	// Step this database back to before the field score, seed what the old
	// economy wrote, and step forward again.
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("down: %v", err)
	}
	u := seedUser(t, pool)
	city := seedCity(t, pool, "Rescore City")
	var visited uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO user_visited_cities (user_id, city_id, city_name, country, latitude, longitude, source, first_visit_at, last_visit_at)
		VALUES ($1, $2, 'Rescore City', 'Testland', 1, 2, 'manual', NOW(), NOW()) RETURNING id`, u, city).Scan(&visited); err != nil {
		t.Fatalf("seed visited city: %v", err)
	}
	for _, r := range []struct {
		kind Kind
		ref  string
		pts  int
	}{
		{KindDailyCheckIn, "checkin:2026-10-01", 5},
		{KindDailySearch, "search:2026-10-01", 5},
		{KindNewCity, "city:" + visited.String(), 50},
		{KindTripCompleted, "trip:" + uuid.NewString(), 100},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO points_events (user_id, kind, ref_key, points, local_date) VALUES ($1, $2, $3, $4, '2026-10-01')`,
			u, r.kind, r.ref, r.pts); err != nil {
			t.Fatalf("seed %d: %v", r.kind, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_progress (user_id, total_points, current_streak) VALUES ($1, 160, 4)`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}

	repo := NewRepository(pool)
	totals, err := repo.Totals(ctx, u)
	if err != nil || totals.TotalPoints != 20+25 || totals.CurrentStreak != 0 {
		t.Fatalf("totals = %+v, %v; want 45 (city 20 + trip 25), streak cleared", totals, err)
	}
	history, err := repo.History(ctx, u, 10, time.Time{}, true)
	if err != nil || len(history) != 2 {
		t.Fatalf("field history = %d rows, %v; retired kinds are left out", len(history), err)
	}
	var key string
	var cityID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT ref_key, city_id FROM points_events WHERE user_id = $1 AND kind = 4`, u).Scan(&key, &cityID); err != nil {
		t.Fatal(err)
	}
	if key != "city:"+city.String() || cityID == nil || *cityID != city {
		t.Fatalf("new city row = %q on %v; re-keyed by the shared city", key, cityID)
	}
	week := SeasonID(day(1))
	scores, err := repo.SeasonScores(ctx, u, city, []int{week})
	if err != nil || scores[week].Score != 20 {
		t.Fatalf("backfilled city week = %+v, %v", scores, err)
	}
}

func migrationHalves(t *testing.T) (up, down string) {
	t.Helper()
	raw, err := os.ReadFile("../../../pkg/db/migrations/0116_field_score.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, "-- +goose Down")
	if i < 0 {
		t.Fatal("no down half")
	}
	return s[:i], s[i:]
}

func TestRepositoryPushSlotIsOncePerDay(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u := seedUser(t, pool)
	first, err := repo.ClaimPushSlot(ctx, u, "passed", day(1))
	if err != nil || !first {
		t.Fatalf("first = %v, %v", first, err)
	}
	second, err := repo.ClaimPushSlot(ctx, u, "passed", day(1))
	if err != nil || second {
		t.Fatalf("second = %v, %v; want false", second, err)
	}
	if next, _ := repo.ClaimPushSlot(ctx, u, "passed", day(2)); !next {
		t.Fatalf("a new day gets a new slot")
	}
}
