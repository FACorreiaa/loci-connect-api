//go:build integration

package tripbrief

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

// now is a Tuesday, 09:30 UTC (10:30 in Lisbon, on summer time until the 25th).
var now = time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)

type traveller struct {
	id uuid.UUID
}

func newTraveller(t *testing.T, zone *string, reminders, device bool) traveller {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.MustPool()
	id := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO users (id, email, timezone) VALUES ($1, $2, $3)`, id, "brief-"+id.String()+"@loci.test", zone)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO notification_settings (user_id, trip_reminders) VALUES ($1, $2)`, id, reminders)
	require.NoError(t, err)
	if device {
		_, err = pool.Exec(ctx, `INSERT INTO push_devices (user_id, platform, endpoint) VALUES ($1, 'apns', $2)`, id, "apns-"+id.String())
		require.NoError(t, err)
	}
	return traveller{id: id}
}

// trip adds a trip from start for days days, with planned days carrying a
// city position, and returns its id.
func (tr traveller) trip(t *testing.T, city string, start time.Time, days int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.MustPool()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO trips (user_id, city_name, title, start_date, end_date)
		VALUES ($1, $2, $2, $3::date, $3::date + ($4 - 1))
		RETURNING id`, tr.id, city, start, days).Scan(&id))
	for d := 1; d <= days; d++ {
		_, err := pool.Exec(ctx, `
			INSERT INTO trip_days (trip_id, day_number, city_name, city_lat, city_lon)
			VALUES ($1, $2, $3, 38.72, -9.14)`, id, d, city)
		require.NoError(t, err)
	}
	return id
}

func dueFor(t *testing.T, repo *Repository, kind string, from, to int, who uuid.UUID) []Candidate {
	t.Helper()
	all, err := repo.Due(context.Background(), now, kind, from, to, 10000)
	require.NoError(t, err)
	var out []Candidate
	for _, c := range all {
		if c.UserID == who {
			out = append(out, c)
		}
	}
	return out
}

func TestDue_OnlyOptedInWithDevice(t *testing.T) {
	repo := NewRepository(testsupport.MustPool())
	yes := newTraveller(t, nil, true, true)
	noDevice := newTraveller(t, nil, true, false)
	off := newTraveller(t, nil, false, true)
	for _, tr := range []traveller{yes, noDevice, off} {
		tr.trip(t, "Lisbon", now.AddDate(0, 0, -1), 3)
	}

	got := dueFor(t, repo, KindMorning, 8, 11, yes.id)
	require.Len(t, got, 1)
	require.Equal(t, 2, got[0].DayNumber)
	require.Equal(t, "Lisbon", got[0].City)
	require.NotNil(t, got[0].DayID)
	require.NotNil(t, got[0].Lat)
	require.Empty(t, dueFor(t, repo, KindMorning, 8, 11, noDevice.id))
	require.Empty(t, dueFor(t, repo, KindMorning, 8, 11, off.id))
}

func TestDue_OnlyDuringTheTrip(t *testing.T) {
	repo := NewRepository(testsupport.MustPool())
	tr := newTraveller(t, nil, true, true)
	tr.trip(t, "Lisbon", now.AddDate(0, 0, 1), 3) // starts tomorrow
	tr.trip(t, "Porto", now.AddDate(0, 0, -5), 3) // ended two days ago
	require.Empty(t, dueFor(t, repo, KindMorning, 8, 11, tr.id))
}

func TestDue_OverlappingTripsOneBrief(t *testing.T) {
	repo := NewRepository(testsupport.MustPool())
	tr := newTraveller(t, nil, true, true)
	tr.trip(t, "Portugal", now.AddDate(0, 0, -4), 10)
	porto := tr.trip(t, "Porto", now.AddDate(0, 0, -1), 2)
	got := dueFor(t, repo, KindMorning, 8, 11, tr.id)
	require.Len(t, got, 1)
	require.Equal(t, porto, got[0].TripID, "the trip that started most recently")
}

func TestLocalToday_ZoneAndBadZoneFallsBackToUTC(t *testing.T) {
	repo := NewRepository(testsupport.MustPool())
	lisbon := "Europe/Lisbon"
	mars := "Mars/Base"
	tl := newTraveller(t, &lisbon, true, true)
	tm := newTraveller(t, &mars, true, true)
	tl.trip(t, "Lisbon", now, 2)
	tm.trip(t, "Lisbon", now, 2)

	l := dueFor(t, repo, KindMorning, 0, 24, tl.id)
	require.Len(t, l, 1)
	require.Equal(t, 10, l[0].LocalHour)
	m := dueFor(t, repo, KindMorning, 0, 24, tm.id)
	require.Len(t, m, 1, "a bad zone must not drop the traveller")
	require.Equal(t, 9, m[0].LocalHour)
	require.Equal(t, "2026-10-20", m[0].LocalDate.Format("2006-01-02"))

	require.Empty(t, dueFor(t, repo, KindMorning, 11, 18, tl.id), "outside the window")
}

func TestClaim_OnceAcrossRacers(t *testing.T) {
	repo := NewRepository(testsupport.MustPool())
	tr := newTraveller(t, nil, true, true)
	tr.trip(t, "Lisbon", now, 2)
	c := dueFor(t, repo, KindMorning, 8, 11, tr.id)[0]

	var wg sync.WaitGroup
	wins := make(chan bool, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.Claim(context.Background(), c, KindMorning, nil)
			require.NoError(t, err)
			wins <- ok
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for ok := range wins {
		if ok {
			n++
		}
	}
	require.Equal(t, 1, n)
	require.Empty(t, dueFor(t, repo, KindMorning, 8, 11, tr.id), "claimed means done for today")
}

func TestDue_WeatherNeedsTheMorningBrief(t *testing.T) {
	repo := NewRepository(testsupport.MustPool())
	tr := newTraveller(t, nil, true, true)
	tr.trip(t, "Lisbon", now, 2)
	require.Empty(t, dueFor(t, repo, KindWeather, 0, 24, tr.id))

	c := dueFor(t, repo, KindMorning, 0, 24, tr.id)[0]
	dry := float32(0.1)
	ok, err := repo.Claim(context.Background(), c, KindMorning, &dry)
	require.NoError(t, err)
	require.True(t, ok)

	w := dueFor(t, repo, KindWeather, 0, 24, tr.id)
	require.Len(t, w, 1)
	p, found, err := repo.MorningPrecip(context.Background(), tr.id, w[0].LocalDate)
	require.NoError(t, err)
	require.True(t, found)
	require.InDelta(t, 0.1, p, 1e-6)
}

func TestStops_InOrderWithOpeningHours(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	repo := NewRepository(pool)
	tr := newTraveller(t, nil, true, true)
	tr.trip(t, "Lisbon", now, 1)
	c := dueFor(t, repo, KindMorning, 0, 24, tr.id)[0]

	var poi uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO points_of_interest (name, location, opening_hours)
		VALUES ($1, ST_SetSRID(ST_MakePoint(-9.14, 38.71), 4326), '"mon-sat 10:00-18:00; sun closed"'::jsonb)
		RETURNING id`, "Gulbenkian-"+uuid.NewString()).Scan(&poi))
	start := 600
	_, err := pool.Exec(ctx, `
		INSERT INTO trip_stops (day_id, poi_id, order_index, name, start_minute) VALUES
			($1, $2, 1, 'Gulbenkian', NULL),
			($1, 'not-a-uuid', 0, 'Manteigaria', $3)`, *c.DayID, poi.String(), start)
	require.NoError(t, err)

	stops, err := repo.Stops(ctx, *c.DayID)
	require.NoError(t, err)
	require.Len(t, stops, 2)
	require.Equal(t, "Manteigaria", stops[0].Name)
	require.Equal(t, 600, *stops[0].StartMinute)
	require.Empty(t, stops[0].OpeningHours)
	closed, known := ClosedOn(stops[1].OpeningHours, time.Sunday)
	require.True(t, known)
	require.True(t, closed)
}

func (tr traveller) joins(t *testing.T, tripID uuid.UUID, role string) {
	t.Helper()
	_, err := testsupport.MustPool().Exec(context.Background(),
		`INSERT INTO trip_members (trip_id, user_id, role) VALUES ($1, $2, $3)`, tripID, tr.id, role)
	require.NoError(t, err)
}

// Planning together: everyone on a trip is travelling it, so each member gets
// the morning brief once a day, like the owner, and the swap link in it. A
// member the owner blocked (or who blocked the owner) gets nothing.
func TestDue_MembersGetTheBriefToo(t *testing.T) {
	repo := NewRepository(testsupport.MustPool())
	owner := newTraveller(t, nil, true, true)
	friend := newTraveller(t, nil, true, true)
	blocked := newTraveller(t, nil, true, true)
	lisbon := owner.trip(t, "Lisbon", now.AddDate(0, 0, -1), 3)
	friend.joins(t, lisbon, "co_planner")
	blocked.joins(t, lisbon, "viewer")
	_, err := testsupport.MustPool().Exec(context.Background(),
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, owner.id, blocked.id)
	require.NoError(t, err)

	got := dueFor(t, repo, KindMorning, 8, 11, friend.id)
	require.Len(t, got, 1)
	require.Equal(t, lisbon, got[0].TripID)
	require.Equal(t, 2, got[0].DayNumber)
	require.Len(t, dueFor(t, repo, KindMorning, 8, 11, owner.id), 1, "the owner still gets theirs")
	require.Empty(t, dueFor(t, repo, KindMorning, 8, 11, blocked.id))

	won, err := repo.Claim(context.Background(), got[0], KindMorning, nil)
	require.NoError(t, err)
	require.True(t, won)
	require.Empty(t, dueFor(t, repo, KindMorning, 8, 11, friend.id), "once a day")
	require.Len(t, dueFor(t, repo, KindMorning, 8, 11, owner.id), 1, "the member's claim is not the owner's")

	// On someone else's trip and their own on the same day, their own wins.
	own := friend.trip(t, "Sintra", now.AddDate(0, 0, -5), 10)
	_, err = testsupport.MustPool().Exec(context.Background(), `DELETE FROM trip_brief_log WHERE user_id = $1`, friend.id)
	require.NoError(t, err)
	got = dueFor(t, repo, KindMorning, 8, 11, friend.id)
	require.Len(t, got, 1)
	require.Equal(t, own, got[0].TripID)
}
