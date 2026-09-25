//go:build integration

package travelhistory

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func newCountryRepo(t *testing.T) (Repository, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testsupport.StartPostgres(t)
	testsupport.Truncate(t, pool, "user_visited_pois", "user_visited_cities")
	return NewRepository(pool, slog.New(slog.NewTextHandler(io.Discard, nil))), pool
}

func seedHistoryUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username) VALUES ($1, $2, $3)",
		id, "th-"+id.String()+"@example.com", "thuser-"+id.String()[:8])
	require.NoError(t, err)
	return id
}

// seedCity inserts a cities row with a centre; the name is made unique so the
// shared container's other rows cannot match by accident.
func seedCity(t *testing.T, pool *pgxpool.Pool, name, country string, lat, lon float64) string {
	t.Helper()
	name = name + "-" + uuid.NewString()[:8]
	_, err := pool.Exec(context.Background(),
		`INSERT INTO cities (name, country, center_location)
		 VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326))`, name, country, lon, lat)
	require.NoError(t, err)
	return name
}

func visitIn(city string, lat, lon float64) VisitInput {
	return VisitInput{
		CityName: city, Latitude: lat, Longitude: lon,
		Source: SourceManual, VisitedAt: time.Now().Add(-48 * time.Hour),
	}
}

// A visit whose caller could not attribute a country (unresolved city_id, no
// cities join) still gets one when the cities table knows the place, so
// "No country recorded yet" stops being the common case on the globe.
func TestRepository_RecordVisitFillsCountryFromCities_Integration(t *testing.T) {
	repo, pool := newCountryRepo(t)
	ctx := context.Background()
	user := seedHistoryUser(t, pool)
	lisbon := seedCity(t, pool, "Globe Lisboa", "Portugal", 38.72, -9.14)
	paris := seedCity(t, pool, "Globe Paris", "France", 48.86, 2.35)

	t.Run("same name near its centre", func(t *testing.T) {
		got, err := repo.RecordVisit(ctx, user, visitIn(lisbon, 38.71, -9.13))
		require.NoError(t, err)
		require.Equal(t, "Portugal", got.Country)
	})

	t.Run("unknown name but within 50 km of a centre", func(t *testing.T) {
		got, err := repo.RecordVisit(ctx, user, visitIn("Globe Cascais Bairro", 38.70, -9.42))
		require.NoError(t, err)
		require.Equal(t, "Portugal", got.Country)
	})

	t.Run("same name but on another continent stays empty", func(t *testing.T) {
		// Paris, Texas must not become France: a name match far from the
		// stored centre is a different place, and we do not guess.
		got, err := repo.RecordVisit(ctx, user, visitIn(paris, 33.66, -95.56))
		require.NoError(t, err)
		require.Equal(t, "", got.Country)
	})

	t.Run("a caller-supplied country wins", func(t *testing.T) {
		in := visitIn(lisbon, 38.71, -9.13)
		in.Country = "Spain"
		other := seedHistoryUser(t, pool)
		got, err := repo.RecordVisit(ctx, other, in)
		require.NoError(t, err)
		require.Equal(t, "Spain", got.Country)
	})
}
