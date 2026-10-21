//go:build integration

package recap

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	os.Exit(m.Run())
}

type seeded struct {
	user, friend, trip, city, poiA, poiB uuid.UUID
}

func exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	_, err := testDB.Exec(context.Background(), sql, args...)
	require.NoError(t, err, sql)
}

// seedTrip builds a finished five-day Lisbon trip with a visit, a done stop,
// taste evidence, points, a badge, a field score, a friend who has Lisbon on
// a friends-visible trip, and a favourite.
func seedTrip(t *testing.T, end time.Time) seeded {
	t.Helper()
	s := seeded{user: uuid.New(), friend: uuid.New(), trip: uuid.New(), city: uuid.New(), poiA: uuid.New(), poiB: uuid.New()}
	start := end.AddDate(0, 0, -4)
	exec(t, `INSERT INTO users (id, email, display_name) VALUES ($1, $2, 'Fernando Correia'), ($3, $4, 'Ana')`,
		s.user, "recap-"+s.user.String()+"@example.com", s.friend, "recap-"+s.friend.String()+"@example.com")
	exec(t, `INSERT INTO user_progress (user_id, timezone) VALUES ($1, 'Europe/Lisbon')`, s.user)
	exec(t, `INSERT INTO cities (id, name, country, center_location)
		VALUES ($1, $2, 'Portugal', ST_SetSRID(ST_MakePoint(-9.14, 38.72), 4326))`, s.city, "Lisbon "+s.city.String()[:8])
	exec(t, `INSERT INTO points_of_interest (id, city_id, name, category, location, neighborhood)
		VALUES ($1, $3, 'Miradouro', 'viewpoint', ST_SetSRID(ST_MakePoint(-9.13, 38.711), 4326), 'Alfama'),
		       ($2, $3, 'Jerónimos', 'museum', ST_SetSRID(ST_MakePoint(-9.206, 38.697), 4326), 'Belém')`,
		s.poiA, s.poiB, s.city)
	cityName := "Lisbon " + s.city.String()[:8]
	exec(t, `INSERT INTO trips (id, user_id, city_id, city_name, title, start_date, end_date)
		VALUES ($1, $2, $3, $4, 'Untitled Trip', $5, $6)`, s.trip, s.user, s.city, cityName, start, end)

	day1, day2 := uuid.New(), uuid.New()
	exec(t, `INSERT INTO trip_days (id, trip_id, day_number, date, city_name, completed_at)
		VALUES ($1, $3, 1, $4, $5, $4), ($2, $3, 2, $6, $5, NULL)`,
		day1, day2, s.trip, start.Add(9*time.Hour), cityName, start.AddDate(0, 0, 1).Add(9*time.Hour))
	stopA, stopB := uuid.New(), uuid.New()
	exec(t, `INSERT INTO trip_stops (id, day_id, poi_id, name, order_index)
		VALUES ($1, $3, $4, 'Miradouro', 0), ($2, $5, $6, 'Jerónimos', 0)`,
		stopA, stopB, day1, s.poiA.String(), day2, s.poiB.String())
	// Stop B done; stop A also visited (the same place twice must count once).
	exec(t, `INSERT INTO trip_stop_marks (stop_id, trip_id, status, marked_at) VALUES ($1, $2, 2, $3), ($4, $2, 2, $3)`,
		stopB, s.trip, start.AddDate(0, 0, 1).Add(11*time.Hour), stopA)
	exec(t, `INSERT INTO user_visited_pois (user_id, poi_id, poi_name, city_name, trip_id, source, visited_at)
		VALUES ($1, $2, 'Miradouro', $3, $4, 'visit_event', $5)`,
		s.user, s.poiA.String(), cityName, s.trip, start.Add(10*time.Hour))

	fb := uuid.New()
	exec(t, `INSERT INTO preference_feedback (id, user_id, poi_id, trip_id, event) VALUES ($1, $2, $3, $4, 'saved')`,
		fb, s.user, s.poiA.String(), s.trip)
	exec(t, `INSERT INTO user_taste_traits (user_id, trait_key, label, score, confidence, evidence_count)
		VALUES ($1, 'category:viewpoint', 'Viewpoints', 0.7, 0.8, 1)`, s.user)
	exec(t, `INSERT INTO taste_trait_evidence (user_id, trait_key, feedback_id, weight, occurred_at)
		VALUES ($1, 'category:viewpoint', $2, 1.5, $3)`, s.user, fb, start)

	exec(t, `INSERT INTO points_events (user_id, kind, ref_key, points, local_date, season_id)
		VALUES ($1, 11, 'a', 8, $2, 1), ($1, 11, 'b', 8, $3, 1), ($1, 1, 'old', 5, $4, 1)`,
		s.user, start, start.AddDate(0, 0, 1), start.AddDate(0, 0, -30))
	exec(t, `INSERT INTO user_badges (user_id, badge_id, awarded_at) VALUES ($1, 'first-city', $2)`, s.user, start.Add(12*time.Hour))
	exec(t, `INSERT INTO field_city_totals (user_id, city_id, score) VALUES ($1, $2, 140)`, s.user, s.city)

	exec(t, `INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2), ($2, $1)`, s.user, s.friend)
	exec(t, `INSERT INTO trips (user_id, city_name, title, visibility) VALUES ($1, $2, 'Ana in Lisbon', 2)`, s.friend, cityName)

	exec(t, `INSERT INTO user_favorites (user_id, item_id, item_name, content_type, city_name, category, added_at)
		VALUES ($1, $2, 'Jerónimos', 'poi', $3, 'museum', $4)`, s.user, s.poiB.String(), cityName, start.Add(15*time.Hour))
	return s
}

func midnightUTC(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestRepositoryTripRecapLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB)
	now := time.Now()
	end := midnightUTC(now.AddDate(0, 0, -2))
	s := seedTrip(t, end)

	// The runner finds it.
	cands, err := repo.Candidates(ctx, now.AddDate(0, 0, -15), now.AddDate(0, 0, 1), 1000)
	require.NoError(t, err)
	var c Candidate
	for _, cand := range cands {
		if cand.TripID == s.trip {
			c = cand
		}
	}
	require.Equal(t, s.trip, c.TripID, "finished trip is a candidate")
	assert.Equal(t, "Europe/Lisbon", c.Timezone)
	assert.Equal(t, 2, c.Days)
	assert.Equal(t, 1, c.DaysDone)
	start, stop, due := c.Due(now)
	require.True(t, due)

	queued, err := repo.Queue(ctx, c, start, stop)
	require.NoError(t, err)
	require.True(t, queued)
	queued, err = repo.Queue(ctx, c, start, stop)
	require.NoError(t, err)
	assert.False(t, queued, "one recap per trip")

	// Facts.
	f, err := repo.LoadTripFacts(ctx, s.user, s.trip, start, stop)
	require.NoError(t, err)
	assert.Equal(t, "Fernando Correia", f.Owner.DisplayName)
	require.Len(t, f.Cities, 1)
	assert.True(t, f.Cities[0].HasCoords)
	assert.Equal(t, "Portugal", f.Cities[0].Country)
	assert.Len(t, f.Places, 2, "visited and marked done once each, de-duplicated")
	assert.Equal(t, 2, f.StopsDone)
	require.Len(t, f.Days, 2)
	assert.True(t, f.Days[0].Completed)
	require.Len(t, f.TripTaste, 1)
	assert.Equal(t, "Viewpoints", f.TripTaste[0].Label)
	assert.Equal(t, 1, f.TripTaste[0].Saves)
	assert.Equal(t, int64(16), f.Points, "only points on the trip's dates")
	assert.Equal(t, []string{"first-city"}, f.Badges)
	require.NotNil(t, f.Field)
	assert.Equal(t, int64(140), f.Field.Score)
	require.Len(t, f.Friends, 1)
	assert.Equal(t, "Ana", f.Friends[0].DisplayName)
	require.NotEmpty(t, f.Highlights)
	assert.Equal(t, "Jerónimos", f.Highlights[0].Name)
	hoods := map[string]bool{}
	for _, p := range f.Places {
		hoods[p.Neighborhood] = true
	}
	assert.True(t, hoods["Alfama"] && hoods["Belém"])

	_, err = repo.LoadTripFacts(ctx, uuid.New(), s.trip, start, stop)
	assert.ErrorIs(t, err, ErrNotFound, "not the caller's trip")

	// Build through the service against the real repository.
	svc := NewService(repo, "https://lociai.fyi", nil, nil)
	pending, err := repo.Pending(ctx, now.Add(time.Minute), 1000)
	require.NoError(t, err)
	var row Row
	for _, p := range pending {
		if p.TripID == s.trip {
			row = p
		}
	}
	require.Equal(t, s.trip, row.TripID)
	status, payload, reason, err := svc.build(ctx, row.UserID, row.TripID, row.PeriodStart, row.PeriodEnd)
	require.NoError(t, err)
	require.Equal(t, StatusReady, status, reason)
	require.NoError(t, repo.SaveBuilt(ctx, row.ID, status, payload, reason))

	notices, err := repo.Unnotified(ctx, 1000)
	require.NoError(t, err)
	found := false
	for _, n := range notices {
		if n.RecapID == row.ID {
			found = true
			assert.Equal(t, s.trip, n.TripID)
		}
	}
	assert.True(t, found)
	claimed, err := repo.ClaimNotify(ctx, row.ID)
	require.NoError(t, err)
	assert.True(t, claimed)
	claimed, err = repo.ClaimNotify(ctx, row.ID)
	require.NoError(t, err)
	assert.False(t, claimed, "at most one push")

	// Share, freeze, unshare.
	code, err := repo.Share(ctx, s.user, row.ID, "code-"+row.ID.String()[:6])
	require.NoError(t, err)
	got, err := repo.ByShareCode(ctx, code, true)
	require.NoError(t, err)
	assert.Equal(t, row.ID, got.ID)

	again, err := repo.UpsertTrip(ctx, s.user, s.trip, start, stop, StatusIneligible, nil, "x")
	require.NoError(t, err)
	assert.Equal(t, StatusReady, again.Status, "a shared recap is never rebuilt")

	_, err = repo.Share(ctx, uuid.New(), row.ID, "other")
	assert.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, repo.Unshare(ctx, s.user, row.ID))
	_, err = repo.ByShareCode(ctx, code, false)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.MarkViewed(ctx, s.user, row.ID))
	list, err := repo.List(ctx, s.user, 10, time.Time{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.NotNil(t, list[0].ViewedAt)

	// Rebuilt on request, now unshared: the upsert keeps the first ready_at.
	firstReady := *list[0].ReadyAt
	re, err := repo.UpsertTrip(ctx, s.user, s.trip, start, stop, StatusReady, payload, "")
	require.NoError(t, err)
	assert.WithinDuration(t, firstReady, *re.ReadyAt, time.Millisecond)
	assert.NotNil(t, re.NotifiedAt)
}

func TestRepositoryRequeueAfterActivity(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB)
	now := time.Now()
	end := midnightUTC(now.AddDate(0, 0, -2))
	s := seedTrip(t, end)
	since := now.AddDate(0, 0, -(LookbackDays + 1))
	start := end.AddDate(0, 0, -4)

	row, err := repo.UpsertTrip(ctx, s.user, s.trip, start, end, StatusIneligible, nil, ReasonNoActivity)
	require.NoError(t, err)
	requeued := func() bool {
		t.Helper()
		_, err := repo.Requeue(ctx, since, 1000)
		require.NoError(t, err)
		got, err := repo.ByID(ctx, s.user, row.ID)
		require.NoError(t, err)
		return got.Status == StatusPending
	}
	assert.False(t, requeued(), "seeded activity predates the build")

	// A stop marked done after the build.
	var day uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx, `SELECT id FROM trip_days WHERE trip_id = $1 AND day_number = 2`, s.trip).Scan(&day))
	stop := uuid.New()
	exec(t, `INSERT INTO trip_stops (id, day_id, poi_id, name, order_index) VALUES ($1, $2, $3, 'Late', 1)`, stop, day, s.poiB.String())
	exec(t, `INSERT INTO trip_stop_marks (stop_id, trip_id, status) VALUES ($1, $2, 2)`, stop, s.trip)
	assert.True(t, requeued())

	// Built again: the same mark does not requeue it twice.
	require.NoError(t, repo.SaveBuilt(ctx, row.ID, StatusIneligible, nil, ReasonNoActivity))
	assert.False(t, requeued())

	// A finished day counts too, but not on a trip past the lookback.
	exec(t, `UPDATE trip_days SET completed_at = NOW() WHERE id = $1`, day)
	_, err = repo.Requeue(ctx, now.AddDate(0, 0, 1), 1000)
	require.NoError(t, err)
	got, err := repo.ByID(ctx, s.user, row.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusIneligible, got.Status, "ended before since")
	assert.True(t, requeued())
}

func TestRepositoryUndatedTripIsNotACandidate(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB)
	user, trip := uuid.New(), uuid.New()
	exec(t, `INSERT INTO users (id, email) VALUES ($1, $2)`, user, "recap-"+user.String()+"@example.com")
	exec(t, `INSERT INTO trips (id, user_id, city_name, title) VALUES ($1, $2, 'Nowhere', 'Someday')`, trip, user)
	exec(t, `INSERT INTO trip_days (trip_id, day_number) VALUES ($1, 1)`, trip)

	now := time.Now()
	cands, err := repo.Candidates(ctx, now.AddDate(0, 0, -15), now.AddDate(0, 0, 1), 1000)
	require.NoError(t, err)
	for _, c := range cands {
		assert.NotEqual(t, trip, c.TripID)
	}
	c, err := repo.TripCandidate(ctx, user, trip)
	require.NoError(t, err)
	_, _, ended := c.Ended(now)
	assert.False(t, ended)
}
