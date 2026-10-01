//go:build integration

package gamification

import (
	"context"
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

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func TestRepositoryAwardsOnceAndCaps(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u := seedUser(t, pool)

	e := Event{UserID: u, Kind: KindDailyCheckIn, RefKey: "checkin:2026-10-01", Points: 5, Label: "Daily check-in", LocalDate: day(1)}
	ok, totals, err := repo.Insert(ctx, e, 0, true)
	if err != nil || !ok || totals.TotalPoints != 5 || totals.CurrentStreak != 1 {
		t.Fatalf("first insert = %v, %+v, %v", ok, totals, err)
	}
	ok, totals, err = repo.Insert(ctx, e, 0, true)
	if err != nil || ok || totals.TotalPoints != 5 {
		t.Fatalf("repeat insert = %v, %+v, %v; want nothing new", ok, totals, err)
	}
	next := e
	next.RefKey, next.LocalDate = "checkin:2026-10-02", day(2)
	if _, totals, err = repo.Insert(ctx, next, 0, true); err != nil || totals.CurrentStreak != 2 || totals.LongestStreak != 2 {
		t.Fatalf("next day = %+v, %v", totals, err)
	}

	for i := 0; i < 3; i++ {
		v := Event{UserID: u, Kind: KindPlaceVisited, RefKey: "poi:" + uuid.NewString() + ":2026-10-02", Points: 10, LocalDate: day(2)}
		inserted, _, err := repo.Insert(ctx, v, 2, false)
		if err != nil {
			t.Fatal(err)
		}
		if want := i < 2; inserted != want {
			t.Fatalf("visit %d inserted = %v, want %v (cap 2)", i, inserted, want)
		}
	}

	stored, err := repo.Totals(ctx, u)
	if err != nil || stored.TotalPoints != 30 {
		t.Fatalf("stored totals = %+v, %v; want 30 points", stored, err)
	}
	today, err := repo.Today(ctx, u, day(2))
	if err != nil || today[KindDailyCheckIn] != 1 || today[KindPlaceVisited] != 2 {
		t.Fatalf("today = %v, %v", today, err)
	}
	counts, err := repo.Counts(ctx, u, 2)
	if err != nil || counts.Places != 2 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
	history, err := repo.History(ctx, u, 10, time.Time{})
	if err != nil || len(history) != 4 {
		t.Fatalf("history = %d rows, %v; want 4", len(history), err)
	}
}

func TestRepositoryScoresAPeriodAndHonoursVisibility(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	a, b := seedUser(t, pool), seedUser(t, pool)

	for _, e := range []Event{
		{UserID: a, Kind: KindNewCity, RefKey: "city:x", Points: 50, LocalDate: day(1)},
		{UserID: a, Kind: KindNewCity, RefKey: "city:y", Points: 50, LocalDate: day(20)},
		{UserID: b, Kind: KindPlaceVisited, RefKey: "poi:p1:2026-10-01", Points: 10, LocalDate: day(1)},
		{UserID: b, Kind: KindPlaceVisited, RefKey: "poi:p1:2026-10-02", Points: 10, LocalDate: day(2)},
	} {
		if _, _, err := repo.Insert(ctx, e, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	week := PeriodFor(PeriodWeek, day(1))
	points, err := repo.Scores(ctx, []uuid.UUID{a, b}, MetricPoints, week)
	if err != nil || points[a] != 50 || points[b] != 20 {
		t.Fatalf("week points = %v, %v", points, err)
	}
	all, err := repo.Scores(ctx, []uuid.UUID{a, b}, MetricCities, Period{})
	if err != nil || all[a] != 2 || all[b] != 0 {
		t.Fatalf("all-time cities = %v, %v", all, err)
	}
	places, err := repo.Scores(ctx, []uuid.UUID{a, b}, MetricPlaces, Period{})
	if err != nil || places[b] != 1 {
		t.Fatalf("one place visited twice is one place: %v, %v", places, err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO notification_settings (user_id, leaderboard_visible) VALUES ($1, FALSE)`, b); err != nil {
		t.Fatal(err)
	}
	visible, err := repo.Visible(ctx, []uuid.UUID{a, b})
	if err != nil || !visible[a] || visible[b] {
		t.Fatalf("visible = %v, %v; want a (no row: default on) and not b", visible, err)
	}
}

func TestRepositoryCompletesOnlyTheOwnersTripDays(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	owner, other := seedUser(t, pool), seedUser(t, pool)

	var tripID, d1, d2 uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO trips (user_id, city_name, title) VALUES ($1, 'Rome', 'Rome on foot') RETURNING id`, owner).Scan(&tripID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO trip_days (trip_id, day_number) VALUES ($1, 1) RETURNING id`, tripID).Scan(&d1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO trip_days (trip_id, day_number) VALUES ($1, 2) RETURNING id`, tripID).Scan(&d2); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := repo.CompleteDay(ctx, other, tripID.String(), d1.String()); err != ErrNotFound {
		t.Fatalf("someone else's day: err = %v", err)
	}
	newly, done, label, err := repo.CompleteDay(ctx, owner, tripID.String(), d1.String())
	if err != nil || !newly || done || label != "Rome on foot, day 1" {
		t.Fatalf("day 1 = %v %v %q %v", newly, done, label, err)
	}
	newly, done, _, err = repo.CompleteDay(ctx, owner, tripID.String(), d2.String())
	if err != nil || !newly || !done {
		t.Fatalf("day 2 = %v %v %v; want the trip done", newly, done, err)
	}
	newly, _, _, err = repo.CompleteDay(ctx, owner, tripID.String(), d2.String())
	if err != nil || newly {
		t.Fatalf("again = %v %v; want not newly done", newly, err)
	}
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
