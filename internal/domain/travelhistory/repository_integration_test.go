//go:build integration

package travelhistory

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

func newTestRepo(t *testing.T, now time.Time) (*repository, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testsupport.StartPostgres(t)
	return &repository{
		db:     pool,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:    func() time.Time { return now },
	}, pool
}

func seedTraveller(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, username) VALUES ($1, $2, $3)`,
		id, "th-"+id.String()+"@example.com", "th-"+id.String()[:8])
	require.NoError(t, err)
	return id
}

func seedCityVisit(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, name, country string, first time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO user_visited_cities (user_id, city_name, country, latitude, longitude, first_visit_at, last_visit_at)
		VALUES ($1, $2, $3, 10, 10, $4, $4)`, userID, name, country, first)
	require.NoError(t, err)
}

func seedPOIVisit(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, at time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO user_visited_pois (user_id, poi_id, visited_at) VALUES ($1, $2, $3)`,
		userID, uuid.NewString(), at)
	require.NoError(t, err)
}

// *_prev_period used to be the running totals as they stood when the window
// opened, so "current vs prev" could only ever go up. Now both windows are
// counted the same way and a quiet year reads as a drop.
func TestSummary_PeriodWindowsAreCountsNotRunningTotals(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	repo, pool := newTestRepo(t, now)
	user := seedTraveller(t, pool)
	daysAgo := func(d int) time.Time { return now.AddDate(0, 0, -d) }

	// Older than both windows (period 100 days → windows [now-100, now] and [now-200, now-100)).
	seedCityVisit(t, pool, user, "Lisbon", "Portugal", daysAgo(400))
	// Previous window: three cities in two countries, one of them already known.
	seedCityVisit(t, pool, user, "Porto", "Portugal", daysAgo(150))
	seedCityVisit(t, pool, user, "Madrid", "Spain", daysAgo(160))
	seedCityVisit(t, pool, user, "Seville", "Spain", daysAgo(170))
	// This window: one city, in a country first reached in this window.
	seedCityVisit(t, pool, user, "Paris", "France", daysAgo(10))

	seedPOIVisit(t, pool, user, daysAgo(400))
	seedPOIVisit(t, pool, user, daysAgo(150))
	seedPOIVisit(t, pool, user, daysAgo(150).Add(time.Hour))
	seedPOIVisit(t, pool, user, daysAgo(5))

	s, err := repo.Summary(context.Background(), user, 100)
	require.NoError(t, err)

	assert.EqualValues(t, 5, s.CitiesVisited, "totals stay all-time")
	assert.EqualValues(t, 3, s.CountriesVisited)
	assert.EqualValues(t, 4, s.POIsVisited)
	assert.EqualValues(t, 100, s.PeriodDays)

	assert.EqualValues(t, 1, s.CitiesVisitedThisPeriod)
	assert.EqualValues(t, 3, s.CitiesVisitedPrev, "not the 4 known when the window opened")
	assert.EqualValues(t, 1, s.CountriesVisitedThisPeriod, "France")
	assert.EqualValues(t, 1, s.CountriesVisitedPrev, "Spain; Portugal was first reached earlier")
	assert.EqualValues(t, 1, s.POIsVisitedThisPeriod)
	assert.EqualValues(t, 2, s.POIsVisitedPrev)
}

func TestSummary_EmptyHistoryIsZero(t *testing.T) {
	repo, pool := newTestRepo(t, time.Now())
	s, err := repo.Summary(context.Background(), seedTraveller(t, pool), 0)
	require.NoError(t, err)
	assert.EqualValues(t, DefaultPeriodDays, s.PeriodDays)
	assert.Zero(t, s.CitiesVisitedThisPeriod+s.CitiesVisitedPrev+s.CountriesVisitedThisPeriod+
		s.CountriesVisitedPrev+s.POIsVisitedThisPeriod+s.POIsVisitedPrev)
}

// GlobeArc carries the leg's own id and travel time so a client can key a
// leg stably and label it "2h 5m" without inventing either.
func TestGlobeData_ArcsCarryLegIDAndDuration(t *testing.T) {
	repo, pool := newTestRepo(t, time.Now())
	ctx := context.Background()
	user := seedTraveller(t, pool)

	var tripID, legID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO trips (user_id, city_name, title) VALUES ($1, 'Lisbon', 'Iberia') RETURNING id`, user).Scan(&tripID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO trip_legs (trip_id, after_day, from_name, to_name, from_lat, from_lon, to_lat, to_lon, distance_km, duration_mins, mode)
		VALUES ($1, 1, 'Lisbon', 'Porto', 38.72, -9.14, 41.15, -8.61, 274, 170, 'rail') RETURNING id`, tripID).Scan(&legID))

	_, arcs, err := repo.GlobeData(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, arcs, 1)
	assert.Equal(t, legID, arcs[0].ID)
	assert.EqualValues(t, 170, arcs[0].DurationMins)
	assert.Equal(t, "rail", arcs[0].Mode)

	p := arcToProto(arcs[0])
	assert.Equal(t, legID.String(), p.GetId())
	assert.EqualValues(t, 170, p.GetDurationMins())
}
