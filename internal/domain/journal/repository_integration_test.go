//go:build integration

package journal

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

type fixture struct {
	user, trip, day1, day2, stop1, stop2 uuid.UUID
}

// newFixture is a Lisbon trip on 20–21 Oct for a traveller in Lisbon time:
// day 1 has Belém (rated Loved) and Alfama (marked done), day 2 is empty.
func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.MustPool()
	f := fixture{user: uuid.New()}
	_, err := pool.Exec(ctx, `INSERT INTO users (id, email, timezone) VALUES ($1, $2, 'Europe/Lisbon')`, f.user, "journal-"+f.user.String()+"@loci.test")
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO trips (user_id, city_name, title, start_date, end_date)
		VALUES ($1, 'Lisbon', 'Lisbon weekend', '2026-10-20', '2026-10-21') RETURNING id`, f.user).Scan(&f.trip))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO trip_days (trip_id, day_number, date, city_name) VALUES ($1, 1, '2026-10-20', 'Lisbon') RETURNING id`, f.trip).Scan(&f.day1))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO trip_days (trip_id, day_number, date, city_name) VALUES ($1, 2, '2026-10-21', 'Lisbon') RETURNING id`, f.trip).Scan(&f.day2))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO trip_stops (day_id, poi_id, order_index, name, start_minute, notes)
		VALUES ($1, 'poi-belem', 0, 'Belém', 600, 'Get there early') RETURNING id`, f.day1).Scan(&f.stop1))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO trip_stops (day_id, poi_id, order_index, name) VALUES ($1, 'poi-alfama', 1, 'Alfama') RETURNING id`, f.day1).Scan(&f.stop2))
	_, err = pool.Exec(ctx, `INSERT INTO trip_stop_marks (stop_id, trip_id, status, marked_at) VALUES ($1, $2, 2, '2026-10-20 15:00Z')`, f.stop2, f.trip)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO user_poi_rankings (user_id, poi_id, poi_name, city_key, city_name, verdict, position)
		VALUES ($1, 'poi-belem', 'Belém', 'lisbon', 'Lisbon', 1, 0)`, f.user)
	require.NoError(t, err)
	// Belém visited on the plan; a bar off the plan at 23:30 UTC on the 20th,
	// which is 00:30 on the 21st in Lisbon.
	_, err = pool.Exec(ctx, `
		INSERT INTO user_visited_pois (user_id, poi_id, poi_name, city_name, trip_id, source, visited_at) VALUES
		($1, 'poi-belem', 'Belém', 'Lisbon', $2, 'visit_event', '2026-10-20 10:05Z'),
		($1, 'poi-bar', 'A Ginjinha', 'Lisbon', $2, 'manual', '2026-10-20 23:30Z')`, f.user, f.trip)
	require.NoError(t, err)
	return f
}

func TestGetTripJournal_MergesStopsVisitsRankingsPhotos(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repo := NewRepository(testsupport.MustPool())
	require.NoError(t, repo.SetDiary(ctx, f.trip, f.day1, "Arrived in the rain."))
	stop := f.stop1
	ready := Photo{ID: uuid.New(), UserID: f.user, TripID: f.trip, StopID: &stop, Key: "trips/" + uuid.NewString() + ".jpg", ContentType: "image/jpeg", Size: 10}
	require.NoError(t, repo.InsertPhoto(ctx, ready))
	_, err := repo.MarkReady(ctx, ready.ID, "Tower")
	require.NoError(t, err)
	require.NoError(t, repo.InsertPhoto(ctx, Photo{ID: uuid.New(), UserID: f.user, TripID: f.trip, Key: "trips/" + uuid.NewString() + ".jpg", ContentType: "image/jpeg", Size: 10}))

	j, err := NewService(repo, &fakeSigner{}, nil).Journal(ctx, f.user, f.trip)
	require.NoError(t, err)
	require.Equal(t, "Lisbon weekend", j.GetTitle())
	require.Len(t, j.GetDays(), 2)

	d1 := j.GetDays()[0]
	require.Equal(t, "Arrived in the rain.", d1.GetDiary())
	require.Len(t, d1.GetStops(), 2)
	belem, alfama := d1.GetStops()[0], d1.GetStops()[1]
	require.Equal(t, "Belém", belem.GetName())
	require.Equal(t, int32(600), belem.GetStartMinute())
	require.Equal(t, "Get there early", belem.GetNotes())
	require.Equal(t, time.Date(2026, 10, 20, 10, 5, 0, 0, time.UTC), belem.GetVisitedAt().AsTime())
	require.EqualValues(t, 1, belem.GetVerdict())
	require.Len(t, belem.GetPhotos(), 1)
	require.Equal(t, "Tower", belem.GetPhotos()[0].GetCaption())
	require.Equal(t, time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC), alfama.GetVisitedAt().AsTime(), "a done mark counts as visited")
	require.Empty(t, d1.GetExtraVisits())

	d2 := j.GetDays()[1]
	require.Len(t, d2.GetExtraVisits(), 1, "00:30 local on the 21st lands on day 2")
	require.Equal(t, "A Ginjinha", d2.GetExtraVisits()[0].GetName())
	require.Empty(t, j.GetPhotos(), "the pending photo never shows")

	_, err = NewService(repo, nil, nil).Journal(ctx, uuid.New(), f.trip)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDiary_SurvivesAndScopes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repo := NewRepository(testsupport.MustPool())

	_, err := repo.DayTrip(ctx, uuid.New(), f.day1)
	require.ErrorIs(t, err, ErrNotFound)
	tripID, err := repo.DayTrip(ctx, f.user, f.day1)
	require.NoError(t, err)
	require.Equal(t, f.trip, tripID)

	require.NoError(t, repo.SetDiary(ctx, f.trip, f.day1, "one"))
	require.NoError(t, repo.SetDiary(ctx, f.trip, f.day1, "two"))
	days, err := repo.Days(ctx, f.user, f.trip)
	require.NoError(t, err)
	require.Equal(t, "two", days[0].Diary)

	require.NoError(t, repo.SetDiary(ctx, f.trip, f.day1, ""))
	days, err = repo.Days(ctx, f.user, f.trip)
	require.NoError(t, err)
	require.Empty(t, days[0].Diary)

	require.ErrorIs(t, repo.DayOnTrip(ctx, f.trip, uuid.New()), ErrNotFound)
	day, err := repo.StopDay(ctx, f.trip, f.stop1)
	require.NoError(t, err)
	require.Equal(t, f.day1, day)
	_, err = repo.StopDay(ctx, uuid.New(), f.stop1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSweeper_RemovesStalePendingAndTombstonesObjects(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	pool := testsupport.MustPool()
	repo := NewRepository(pool)

	stale := Photo{ID: uuid.New(), UserID: f.user, TripID: f.trip, Key: "trips/stale-" + uuid.NewString() + ".jpg", ContentType: "image/jpeg", Size: 1}
	fresh := Photo{ID: uuid.New(), UserID: f.user, TripID: f.trip, Key: "trips/fresh-" + uuid.NewString() + ".jpg", ContentType: "image/jpeg", Size: 1}
	require.NoError(t, repo.InsertPhoto(ctx, stale))
	require.NoError(t, repo.InsertPhoto(ctx, fresh))
	_, err := pool.Exec(ctx, `UPDATE trip_photos SET created_at = NOW() - INTERVAL '2 days' WHERE id = $1`, stale.ID)
	require.NoError(t, err)

	signer := &recordingSigner{}
	sw := NewSweeper(repo, signer, nil, nil)
	// Run as if past the tombstone grace, so the swept row's object goes too.
	require.NoError(t, sw.Sweep(ctx, time.Now().Add(tombstoneGrace+time.Minute)))

	_, err = repo.Photo(ctx, f.user, stale.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.Photo(ctx, f.user, fresh.ID)
	require.NoError(t, err)
	require.Contains(t, signer.deleted, stale.Key)
	require.NotContains(t, signer.deleted, fresh.Key)

	// Deleting the trip cascades the photo row; the trigger tombstones its
	// object and the next sweep removes it.
	_, err = pool.Exec(ctx, `DELETE FROM trips WHERE id = $1`, f.trip)
	require.NoError(t, err)
	// A tombstone younger than the upload URL is left for a later sweep.
	keys, err := repo.Tombstones(ctx, time.Now().Add(-tombstoneGrace), 10000)
	require.NoError(t, err)
	require.NotContains(t, keys, fresh.Key)
	require.NoError(t, sw.Sweep(ctx, time.Now().Add(tombstoneGrace+time.Minute)))
	require.Contains(t, signer.deleted, fresh.Key)
	keys, err = repo.Tombstones(ctx, time.Now().Add(time.Hour), 10000)
	require.NoError(t, err)
	require.NotContains(t, keys, fresh.Key)
	require.NotContains(t, keys, stale.Key)
}
