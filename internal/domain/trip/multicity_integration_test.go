//go:build integration

package trip

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTripDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testTripDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func newTripUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testTripDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, email)
	require.NoError(t, err)
	return id
}

func f64(v float64) *float64 { return &v }

// A multi-city trip has to survive the round trip through the database: which
// city each day is spent in, and the travel between them. Before this, a trip
// was single-city by construction and "do both" could only be notes in a stop.
func TestRepository_MultiCityTripRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "multicity-"+uuid.NewString()+"@loci.test")

	start := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	day2Date := start.AddDate(0, 0, 1)

	in := &Trip{
		UserID:   userID,
		CityName: "Évora", // primary city, used for titles and exports
		Title:    "Évora and Beja",
		Days: []TripDay{
			{
				DayNumber: 1,
				Date:      &start,
				CityName:  "Évora",
				CityLat:   f64(38.5714),
				CityLon:   f64(-7.9135),
				TravelDay: true,
				Stops:     []TripStop{{Name: "Roman Temple", OrderIndex: 0}},
			},
			{
				DayNumber: 2,
				Date:      &day2Date,
				CityName:  "Beja",
				CityLat:   f64(38.0153),
				CityLon:   f64(-7.8624),
				TravelDay: true,
				Stops:     []TripStop{{Name: "Beja Castle", OrderIndex: 0}},
			},
		},
		Legs: []TripLeg{
			{
				AfterDay: 0, FromName: "Porto", ToName: "Évora",
				FromLat: f64(41.15), FromLon: f64(-8.61),
				ToLat: f64(38.5714), ToLon: f64(-7.9135),
				DistanceKm: 290, DurationMins: 217, Mode: "drive",
			},
			{
				AfterDay: 1, FromName: "Évora", ToName: "Beja",
				FromLat: f64(38.5714), FromLon: f64(-7.9135),
				ToLat: f64(38.0153), ToLon: f64(-7.8624),
				DistanceKm: 62, DurationMins: 46, Mode: "drive",
			},
		},
	}

	saved, err := repo.SaveTrip(ctx, in, 0)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, saved.ID)

	got, err := repo.GetTrip(ctx, saved.ID, userID)
	require.NoError(t, err)

	t.Run("each day keeps its own city", func(t *testing.T) {
		require.Len(t, got.Days, 2)
		assert.Equal(t, "Évora", got.Days[0].CityName)
		assert.Equal(t, "Beja", got.Days[1].CityName)
		require.NotNil(t, got.Days[1].CityLat)
		assert.InDelta(t, 38.0153, *got.Days[1].CityLat, 0.0001)
		assert.True(t, got.Days[1].TravelDay)
	})

	t.Run("legs survive in route order", func(t *testing.T) {
		require.Len(t, got.Legs, 2)
		assert.Equal(t, int32(0), got.Legs[0].AfterDay, "outbound leg comes first")
		assert.Equal(t, "Porto", got.Legs[0].FromName)
		assert.Equal(t, "Beja", got.Legs[1].ToName)
		assert.Equal(t, int32(46), got.Legs[1].DurationMins)
		assert.Equal(t, "drive", got.Legs[1].Mode)
	})

	t.Run("legs are replaced rather than accumulated on re-save", func(t *testing.T) {
		got.Legs = got.Legs[:1]
		again, err := repo.SaveTrip(ctx, got, got.Version)
		require.NoError(t, err)

		reread, err := repo.GetTrip(ctx, again.ID, userID)
		require.NoError(t, err)
		assert.Len(t, reread.Legs, 1, "a shortened route must not leave the old leg behind")
	})
}

// A trip written before multi-city support has no day city and no legs. It must
// still load, with the day city simply empty.
func TestRepository_SingleCityTripStillLoads(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testTripDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := newTripUser(t, "singlecity-"+uuid.NewString()+"@loci.test")

	saved, err := repo.SaveTrip(ctx, &Trip{
		UserID:   userID,
		CityName: "Lisbon",
		Title:    "Lisbon weekend",
		Days: []TripDay{
			{DayNumber: 1, Stops: []TripStop{{Name: "Alfama", OrderIndex: 0}}},
		},
	}, 0)
	require.NoError(t, err)

	got, err := repo.GetTrip(ctx, saved.ID, userID)
	require.NoError(t, err)
	require.Len(t, got.Days, 1)
	assert.Empty(t, got.Days[0].CityName, "an unset day city means the trip's primary city")
	assert.Empty(t, got.Legs)
	assert.Equal(t, "Lisbon", got.CityName)
}
