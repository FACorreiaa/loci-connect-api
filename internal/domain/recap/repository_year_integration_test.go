//go:build integration

package recap

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedYear adds to seedTrip's Lisbon trip (11–15 Oct 2026): a Porto trip in
// March, a visit last year that must not count, taste evidence before and
// during the year, a review and a public list, and the traveller's globe
// with Portugal first visited in 2024.
func seedYear(t *testing.T) (seeded, string) {
	t.Helper()
	s := seedTrip(t, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC))
	lisbon := "Lisbon " + s.city.String()[:8]

	porto := uuid.New()
	exec(t, `INSERT INTO trips (id, user_id, city_name, title, start_date, end_date)
		VALUES ($1, $2, 'Porto', 'Porto weekend', '2026-03-02', '2026-03-05')`, porto, s.user)
	exec(t, `INSERT INTO trip_days (trip_id, day_number, date, city_name, city_lat, city_lon)
		VALUES ($1, 1, '2026-03-02 09:00Z', 'Porto', 41.15, -8.61), ($1, 2, '2026-03-03 09:00Z', 'Porto', 41.15, -8.61)`, porto)
	// Last year: out.
	exec(t, `INSERT INTO user_visited_pois (user_id, poi_id, poi_name, city_name, source, visited_at)
		VALUES ($1, 'old-poi', 'Old place', 'Porto', 'visit_event', '2025-06-01 12:00Z')`, s.user)
	// 31 Dec 23:30 in Lisbon is 2026 there and in UTC: in. 1 Jan 00:30 UTC
	// next year is out.
	exec(t, `INSERT INTO user_visited_pois (user_id, poi_id, poi_name, city_name, source, visited_at)
		VALUES ($1, 'nye-poi', 'NYE bar', 'Porto', 'visit_event', '2027-01-01 00:30Z')`, s.user)

	exec(t, `INSERT INTO user_visited_cities (user_id, city_name, country, country_code, latitude, longitude, source, first_visit_at, last_visit_at)
		VALUES ($1, $2, 'Portugal', 'PT', 38.72, -9.14, 'visit_event', '2026-10-11', '2026-10-15'),
		       ($1, 'Faro', 'Portugal', 'PT', 37.02, -7.93, 'visit_event', '2024-07-01', '2024-07-03'),
		       ($1, 'Porto', 'Portugal', 'PT', 41.15, -8.61, 'visit_event', '2026-03-02', '2026-03-03')`, s.user, lisbon)
	// A second Lisbon row from another source, with no country, written
	// later: it must not erase Lisbon's country or make Portugal look new.
	exec(t, `INSERT INTO user_visited_cities (user_id, city_name, country, country_code, latitude, longitude, source, first_visit_at, last_visit_at)
		VALUES ($1, $2, '', NULL, 38.72, -9.14, 'manual', '2026-10-12', '2026-10-20')`, s.user, lisbon)

	oldFb := uuid.New()
	exec(t, `INSERT INTO preference_feedback (id, user_id, poi_id, event) VALUES ($1, $2, 'x', 'saved')`, oldFb, s.user)
	exec(t, `INSERT INTO user_taste_traits (user_id, trait_key, label, score, confidence, evidence_count)
		VALUES ($1, 'category:bookshop', 'Bookshops', 0.5, 0.5, 2)`, s.user)
	exec(t, `INSERT INTO taste_trait_evidence (user_id, trait_key, feedback_id, weight, occurred_at)
		VALUES ($1, 'category:bookshop', $2, 2.0, '2025-05-01')`, s.user, oldFb)

	exec(t, `INSERT INTO reviews (user_id, poi_id, rating, content, created_at) VALUES ($1, $2, 5, 'Lovely', '2026-10-12')`, s.user, s.poiA)
	exec(t, `INSERT INTO lists (user_id, name, is_public, is_itinerary, created_at) VALUES ($1, 'Lisbon cafés', TRUE, FALSE, '2026-10-16'),
		($1, 'Private', FALSE, FALSE, '2026-10-16')`, s.user)
	return s, lisbon
}

func TestRepositoryYearFacts(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB)
	s, lisbon := seedYear(t)
	now := time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC)

	f, err := repo.LoadYearFacts(ctx, s.user, 2026, now)
	require.NoError(t, err)
	assert.Equal(t, "Europe/Lisbon", f.Timezone)
	assert.Equal(t, "Fernando Correia", f.Owner.DisplayName)

	require.Len(t, f.Trips, 2)
	byCity := map[string]YearTrip{}
	for _, tr := range f.Trips {
		byCity[tr.CityName] = tr
	}
	assert.Equal(t, 5, byCity[lisbon].Days, "11–15 Oct")
	assert.Equal(t, 4, byCity["Porto"].Days, "2–5 Mar, from its dates")
	assert.Equal(t, "PT", byCity["Porto"].CountryCode)

	cities := map[string]YearCity{}
	for _, c := range f.Cities {
		cities[c.Name] = c
	}
	require.Contains(t, cities, lisbon)
	require.Contains(t, cities, "Porto")
	assert.Equal(t, 2, cities[lisbon].Places, "Miradouro (visited and marked) and Jerónimos")
	assert.Equal(t, 2, cities[lisbon].Days)
	assert.Equal(t, 1, cities[lisbon].Trips)
	assert.Equal(t, "Portugal", cities[lisbon].Country)
	assert.False(t, cities[lisbon].FirstCountryVisit, "Portugal first visited in 2024")
	assert.True(t, cities[lisbon].HasCoords)
	assert.Equal(t, 2, cities["Porto"].Days, "two dated trip days")
	assert.Zero(t, cities["Porto"].Places, "last year's visit and next year's do not count")
	assert.True(t, cities["Porto"].HasCoords)

	assert.Len(t, f.Places, 2)

	traits := map[string]TraitShift{}
	for _, tr := range f.Taste {
		traits[tr.Key] = tr
	}
	assert.InDelta(t, 1.5, traits["category:viewpoint"].Added, 0.001)
	assert.Zero(t, traits["category:viewpoint"].Before)
	assert.InDelta(t, 2.0, traits["category:bookshop"].Before, 0.001)
	assert.Zero(t, traits["category:bookshop"].Added)

	assert.EqualValues(t, 21, f.Points, "every 2026 point, the trip's and the month before")
	assert.Len(t, f.ActiveDates, 3)
	assert.Equal(t, []string{"first-city"}, f.Badges)
	assert.Equal(t, Contributions{Reviews: 1, Lists: 1}, f.Contributions, "the private list is not a contribution")
	assert.Nil(t, f.Percentile, "fewer than 200 active travellers in the test database")

	rec, reason, ok := NewBuilder().BuildYear(f)
	require.True(t, ok, reason)
	assert.Equal(t, "Your 2026", rec.GetTitle())
}

func TestRepositoryYearLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB)
	s, _ := seedYear(t)

	cands, err := repo.YearCandidates(ctx, 2026, []uuid.UUID{s.user}, 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	assert.Equal(t, s.user, cands[0].UserID)
	assert.Equal(t, "Europe/Lisbon", cands[0].Timezone)

	none, err := repo.YearCandidates(ctx, 2026, []uuid.UUID{uuid.New()}, 10)
	require.NoError(t, err)
	assert.Empty(t, none, "only limits")

	queued, err := repo.QueueYear(ctx, s.user, 2026)
	require.NoError(t, err)
	require.True(t, queued)
	queued, err = repo.QueueYear(ctx, s.user, 2026)
	require.NoError(t, err)
	assert.False(t, queued, "one recap per year")

	cands, err = repo.YearCandidates(ctx, 2026, []uuid.UUID{s.user}, 10)
	require.NoError(t, err)
	assert.Empty(t, cands, "a queued year is no longer a candidate")

	row, err := repo.ByYear(ctx, s.user, 2026)
	require.NoError(t, err)
	assert.Equal(t, KindYear, row.Kind)
	assert.Equal(t, 2026, row.Year)
	assert.Equal(t, StatusPending, row.Status)

	require.NoError(t, repo.SaveBuilt(ctx, row.ID, StatusReady, []byte{1}, ""))
	notices, err := repo.Unnotified(ctx, 1000)
	require.NoError(t, err)
	var n Notice
	for _, x := range notices {
		if x.RecapID == row.ID {
			n = x
		}
	}
	require.Equal(t, row.ID, n.RecapID)
	assert.Equal(t, KindYear, n.Kind)
	assert.Equal(t, 2026, n.Year)
	assert.Equal(t, uuid.Nil, n.TripID)

	// A request rebuild keeps the row and marks it notified.
	got, err := repo.UpsertYear(ctx, s.user, 2026, StatusReady, []byte{2}, "")
	require.NoError(t, err)
	assert.Equal(t, row.ID, got.ID)
	assert.NotNil(t, got.NotifiedAt)

	tz, err := repo.Timezone(ctx, s.user)
	require.NoError(t, err)
	assert.Equal(t, "Europe/Lisbon", tz)
	tz, err = repo.Timezone(ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "UTC", tz)
}
