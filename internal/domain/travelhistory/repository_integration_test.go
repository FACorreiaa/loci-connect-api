//go:build integration

package travelhistory

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	travelhistoryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/travelhistory"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
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

// Both RPCs that carry a TravelSummary say the period counts are real, so a
// traveller with nothing this period but something last period reads as a
// drop (0 vs 1), not as the all-time fallback's fake rise.
func TestHandler_SummariesSetHasPeriodCounts(t *testing.T) {
	now := time.Now()
	repo, pool := newTestRepo(t, now)
	user := seedTraveller(t, pool)
	seedCityVisit(t, pool, user, "Porto", "Portugal", now.AddDate(0, 0, -150))

	h := NewHandler(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.WithValue(context.Background(), interceptors.UserIDKey, user.String())

	globe, err := h.GetGlobeData(ctx, connect.NewRequest(&travelhistoryv1.GetGlobeDataRequest{PeriodDays: 100}))
	require.NoError(t, err)
	gs := globe.Msg.GetSummary()
	assert.True(t, gs.GetHasPeriodCounts())
	assert.EqualValues(t, 0, gs.GetCitiesVisitedThisPeriod())
	assert.EqualValues(t, 1, gs.GetCitiesVisitedPrevPeriod())

	summary, err := h.GetTravelSummary(ctx, connect.NewRequest(&travelhistoryv1.GetTravelSummaryRequest{PeriodDays: 100}))
	require.NoError(t, err)
	assert.True(t, summary.Msg.GetSummary().GetHasPeriodCounts())
	assert.EqualValues(t, 0, summary.Msg.GetSummary().GetCitiesVisitedThisPeriod())
}

// RecordVisit hands back the new place visit's id, and nothing for a replay of
// the same visit, so a replay cannot teach taste twice.
func TestRecordVisit_ReturnsVisitIDOnlyWhenInserted(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	user := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)",
		user, "visitid-"+uuid.NewString()+"@loci.test")
	require.NoError(t, err)
	repo := NewRepository(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	in := VisitInput{
		CityName: "Lisbon", Latitude: 38.71, Longitude: -9.14,
		POIID: uuid.NewString(), POIName: "Miradouro",
		VisitedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
	}
	_, first, err := repo.RecordVisit(ctx, user, in)
	require.NoError(t, err)
	require.NotNil(t, first)

	_, replay, err := repo.RecordVisit(ctx, user, in)
	require.NoError(t, err)
	require.Nil(t, replay)
}

// "I've been here" sends no timestamp, so the server stamps now(). Pressing it
// again (on another day, or after walking there) must not add a second visit,
// a second taste signal, or bump the city's count.
func TestRecordVisit_ServerStampedRepeatIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	user := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)",
		user, "idem-"+uuid.NewString()+"@loci.test")
	require.NoError(t, err)
	repo := NewRepository(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	poiID := uuid.NewString()
	in := VisitInput{CityName: "Lisbon-" + uuid.NewString(), Latitude: 38.71, Longitude: -9.14, POIID: poiID, POIName: "Miradouro"}
	_, first, err := repo.RecordVisit(ctx, user, in)
	require.NoError(t, err)
	require.NotNil(t, first)

	_, again, err := repo.RecordVisit(ctx, user, in)
	require.NoError(t, err)
	require.Nil(t, again, "a repeated manual mark must not insert a visit")

	var visits, cityCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_visited_pois WHERE user_id = $1 AND poi_id = $2`, user, poiID).Scan(&visits))
	require.Equal(t, 1, visits)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT visit_count FROM user_visited_cities WHERE user_id = $1 AND city_name = $2`, user, in.CityName).Scan(&cityCount))
	require.Equal(t, 1, cityCount)

	// A timed arrival (walk, trip day) is a real return visit and still counts.
	timed := in
	timed.VisitedAt = time.Now().Add(48 * time.Hour)
	_, returned, err := repo.RecordVisit(ctx, user, timed)
	require.NoError(t, err)
	require.NotNil(t, returned)
}

func TestDeleteVisitedPOI_RemovesOnlyThatVisit(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	user, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{user, other} {
		_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)", id, "del-"+uuid.NewString()+"@loci.test")
		require.NoError(t, err)
	}
	repo := NewRepository(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	city := "Porto-" + uuid.NewString()
	visit := func(u uuid.UUID, poi string) uuid.UUID {
		_, id, err := repo.RecordVisit(ctx, u, VisitInput{CityName: city, Latitude: 41.15, Longitude: -8.61, POIID: poi, POIName: poi})
		require.NoError(t, err)
		require.NotNil(t, id)
		return *id
	}
	first := visit(user, "Ribeira")
	visit(user, "Livraria Lello")
	theirs := visit(other, "Ribeira")

	// Another user's id is not found and is left untouched.
	deleted, err := repo.DeleteVisitedPOI(ctx, user, theirs)
	require.NoError(t, err)
	require.False(t, deleted)

	deleted, err = repo.DeleteVisitedPOI(ctx, user, first)
	require.NoError(t, err)
	require.True(t, deleted)

	var pois, cities int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_visited_pois WHERE user_id = $1`, user).Scan(&pois))
	require.Equal(t, 1, pois, "only the deleted visit goes")
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_visited_cities WHERE user_id = $1`, user).Scan(&cities))
	require.Equal(t, 1, cities, "the city row survives")
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_visited_pois WHERE user_id = $1`, other).Scan(&pois))
	require.Equal(t, 1, pois)
}

func TestRecordVisit_SourceVisitEventPersisted(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	user := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)", user, "src-"+uuid.NewString()+"@loci.test")
	require.NoError(t, err)
	repo := NewRepository(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, id, err := repo.RecordVisit(ctx, user, VisitInput{
		CityName: "Braga", Latitude: 41.55, Longitude: -8.42, POIID: uuid.NewString(), POIName: "Sé",
		Source: SourceVisitEvent, VisitedAt: time.Now(),
	})
	require.NoError(t, err)
	var source string
	require.NoError(t, pool.QueryRow(ctx, `SELECT source FROM user_visited_pois WHERE id = $1`, *id).Scan(&source))
	require.Equal(t, string(SourceVisitEvent), source)
}
