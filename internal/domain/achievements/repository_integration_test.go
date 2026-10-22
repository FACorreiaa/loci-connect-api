//go:build integration

package achievements

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/gamification"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

func newUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, username) VALUES ($1, $2, $3)`,
		id, id.String()+"@example.com", "u"+id.String()[:8])
	require.NoError(t, err)
	return id
}

func run(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err, sql)
}

func award(t *testing.T, pool *pgxpool.Pool, u uuid.UUID, k gamification.Kind, ref string, d time.Time) {
	t.Helper()
	_, _, err := gamification.NewRepository(pool).Insert(context.Background(), gamification.Event{
		UserID: u, Kind: k, RefKey: ref, Points: 1, FieldPoints: 1, Label: ref,
		LocalDate: d, SeasonID: gamification.SeasonID(d),
	}, 0)
	require.NoError(t, err)
}

// tripWithStops makes a trip with one day of n stops, all done when done.
func tripWithStops(t *testing.T, pool *pgxpool.Pool, u uuid.UUID, n int, done bool) (trip, day uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO trips (user_id, title) VALUES ($1, 'Walk') RETURNING id`, u).Scan(&trip))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO trip_days (trip_id, day_number) VALUES ($1, 1) RETURNING id`, trip).Scan(&day))
	for i := range n {
		var stop uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO trip_stops (day_id, name, order_index) VALUES ($1, $2, $3) RETURNING id`,
			day, fmt.Sprintf("Stop %d", i), i).Scan(&stop))
		if done {
			run(t, pool, `INSERT INTO trip_stop_marks (stop_id, trip_id, status) VALUES ($1, $2, 2)`, stop, trip)
		}
	}
	return trip, day
}

// Every counter runs on the real schema, and the ones with logic of their
// own count what they should.
func TestCountersOnTheRealSchema(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	u, friend, follower, invitee := newUser(t, pool), newUser(t, pool), newUser(t, pool), newUser(t, pool)
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

	run(t, pool, `INSERT INTO user_progress (user_id, timezone) VALUES ($1, 'Europe/Lisbon')`, u)
	porto := uuid.New()
	run(t, pool, `INSERT INTO cities (id, name, country) VALUES ($1, 'Porto', 'Portugal')`, porto)
	run(t, pool, `
		INSERT INTO user_visited_cities (user_id, city_id, city_name, country, country_code, latitude, longitude, first_visit_at, last_visit_at) VALUES
		  ($1, NULL, 'Porto', 'Portugal', 'PT', 41.1, -8.6, NOW(), NOW()),
		  ($1, $2, ' porto ', 'Portugal', 'pt', 41.1, -8.6, NOW(), NOW()),
		  ($1, NULL, 'Lisbon', 'Portugal', NULL, 38.7, -9.1, NOW(), NOW()),
		  ($1, NULL, 'Vigo', 'Spain', '', 42.2, -8.7, NOW(), NOW())`, u, porto)
	run(t, pool, `
		INSERT INTO user_visited_pois (user_id, poi_id, city_name, source, visited_at) VALUES
		  ($1, 'a', 'Porto', 'visit_event', '2024-05-01 12:00Z'),
		  ($1, 'a', 'Porto', 'trip',        '2025-05-01 12:00Z'),
		  ($1, 'b', 'Porto', 'trip',        '2026-10-10 01:30Z'),
		  ($1, 'c', 'Lisbon', 'manual',     '2026-10-10 02:00Z')`, u)

	_, fullDay := tripWithStops(t, pool, u, 6, true)
	_, shortDay := tripWithStops(t, pool, u, 3, true)
	_, openDay := tripWithStops(t, pool, u, 6, false)
	for _, d := range []uuid.UUID{fullDay, shortDay, openDay} {
		award(t, pool, u, gamification.KindTripDayCompleted, "tripday:"+d.String(), day)
	}
	tour, _ := tripWithStops(t, pool, u, 1, false)
	run(t, pool, `
		INSERT INTO trip_cities (trip_id, city_name, order_index) VALUES ($1, 'Porto', 0), ($1, 'Braga', 1), ($1, 'Vigo', 2)`, tour)
	award(t, pool, u, gamification.KindTripCompleted, "trip:"+tour.String(), day)
	award(t, pool, u, gamification.KindStopDone, "stop:x", day)
	award(t, pool, u, gamification.KindPlaceVisited, "poi:a:2026-10-12", day)

	run(t, pool, `INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2), ($2, $1), ($3, $1)`, u, friend, follower)
	run(t, pool, `UPDATE users SET invited_by_user_id = $1 WHERE id = $2`, u, invitee)
	city := uuid.New()
	run(t, pool, `INSERT INTO cities (id, name, country) VALUES ($1, 'Keeperville', 'Nowhere')`, city)
	run(t, pool, `INSERT INTO field_city_totals (user_id, city_id, score) VALUES ($1, $2, 650)`, u, city)

	got, err := repo.Counters(ctx, u, 600)
	require.NoError(t, err)
	want := map[Counter]int64{
		CountCities:         3, // Porto twice under two spellings
		CountCountries:      2, // Portugal (by name, with or without a code) and Spain
		CountPlaces:         3,
		CountVerifiedVisits: 1,
		CountDays:           3,
		CountTrips:          1,
		CountStops:          1,
		CountGrandTours:     1,
		CountFullDays:       1, // six done; not three, not six open
		CountHomecomings:    1, // Porto in 2024, 2025, 2026
		CountNightVisits:    1, // 02:30 in Lisbon on a trip; the manual one does not count
		CountFriends:        1,
		CountInvited:        1,
		CountKeeperCities:   1,
		CountWeekStreak:     1,
		CountReviews:        0,
	}
	for c, n := range want {
		assert.Equal(t, n, got[c], "counter %s", c)
	}

	// An unknown zone costs the night secret, not the evaluation.
	run(t, pool, `UPDATE user_progress SET timezone = 'Mars/Olympus' WHERE user_id = $1`, u)
	got, err = repo.Counters(ctx, u, 600)
	require.NoError(t, err)
	assert.Zero(t, got[CountNightVisits])
	assert.Equal(t, int64(3), got[CountCities])
}

// The service on the real schema: a user from before achievements is
// backfilled silently, a later action unlocks live, and rarity counts.
func TestEvaluateBackfillThenLive(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	p := &pushes{}
	s := NewService(repo, p, nil, nil, true)
	s.now = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }

	old := newUser(t, pool)
	run(t, pool, `UPDATE users SET achievements_backfilled_at = NULL WHERE id = $1`, old)
	run(t, pool, `
		INSERT INTO user_visited_cities (user_id, city_name, latitude, longitude, first_visit_at, last_visit_at)
		VALUES ($1, 'Porto', 41.1, -8.6, NOW(), NOW())`, old)

	ids, err := repo.Unbackfilled(ctx, 10_000)
	require.NoError(t, err)
	assert.Contains(t, ids, old)
	n, err := s.BackfillBatch(ctx, 10_000)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, 1)
	assert.Empty(t, p.titles, "the backfill pushed")
	earned, backfilled, err := repo.Earned(ctx, old)
	require.NoError(t, err)
	assert.True(t, backfilled)
	require.Len(t, earned, 1)
	assert.Equal(t, Earned{Unit: Unit{ID: "first-pin"}, At: earned[0].At, Source: SourceBackfill}, earned[0])

	// A new account has no past: its first unlock is live.
	fresh := newUser(t, pool)
	run(t, pool, `
		INSERT INTO user_visited_cities (user_id, city_name, latitude, longitude, first_visit_at, last_visit_at)
		VALUES ($1, 'Lisbon', 38.7, -9.1, NOW(), NOW())`, fresh)
	got, err := s.Evaluate(ctx, fresh)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "first-pin", got[0].GetId())
	assert.Equal(t, []string{"First Pin"}, p.titles)
	again, err := s.Evaluate(ctx, fresh)
	require.NoError(t, err)
	assert.Empty(t, again)

	require.NoError(t, repo.MarkSeen(ctx, fresh, []string{"first-pin"}))
	list, err := s.List(ctx, fresh, fresh)
	require.NoError(t, err)
	assert.Equal(t, int32(1), list.GetEarnedCount())
	for _, a := range list.GetAchievements() {
		assert.False(t, a.GetIsNew(), a.GetId())
	}

	refreshed, err := repo.RefreshRarity(ctx, time.Now().AddDate(-1, 0, 0), time.Now())
	require.NoError(t, err)
	assert.True(t, refreshed)
	refreshed, err = repo.RefreshRarity(ctx, time.Now().AddDate(-1, 0, 0), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.False(t, refreshed, "a fresh count was redone")
	r, err := repo.Rarity(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, r.Holders[Unit{ID: "first-pin"}], 2)
}
