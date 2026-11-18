//go:build integration

package placecheck

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

func TestRepositorySaveAndGet(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	repo := NewRepository(pool)
	now := time.Date(2026, 4, 14, 9, 0, 0, 0, time.UTC)

	// A place the model put in the wrong part of town.
	var poiID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO points_of_interest (name, location)
		VALUES ($1, ST_SetSRID(ST_MakePoint(-9.20, 38.80), 4326)) RETURNING id`,
		"placecheck-"+uuid.NewString()).Scan(&poiID))
	missing := "placecheck-missing-" + uuid.NewString()

	found := Verification{
		POIID: poiID.String(), Matched: true, Source: SourceOSM, ExternalID: "way/11", WikidataID: "Q1542372",
		MatchedName: "Museu Nacional do Azulejo", Kind: "museum", Lat: 38.7251, Lon: -9.1137,
		HoursRaw: "Tu-Su 10:00-18:00", Confidence: 1, CheckedAt: now, ExpiresAt: now.Add(ttlMatched),
	}
	notFound := Verification{POIID: missing, Source: SourceOSM, CheckedAt: now, ExpiresAt: now.Add(ttlUnmatched)}
	require.NoError(t, repo.Save(ctx, []Verification{found, notFound}))

	got, err := repo.Get(ctx, []string{poiID.String(), missing, "never-checked"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, found, got[poiID.String()])
	require.Equal(t, notFound, got[missing])

	// The place now stands where the map says it is.
	var lat, lon float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT ST_Y(location), ST_X(location) FROM points_of_interest WHERE id = $1`, poiID).Scan(&lat, &lon))
	require.InDelta(t, 38.7251, lat, 1e-9)
	require.InDelta(t, -9.1137, lon, 1e-9)

	// A re-check replaces the row; a place that drops off the map loses its pin
	// in this table but keeps its last known position.
	later := now.Add(31 * 24 * time.Hour)
	gone := Verification{POIID: poiID.String(), Source: SourceOSM, CheckedAt: later, ExpiresAt: later.Add(ttlUnmatched)}
	require.NoError(t, repo.Save(ctx, []Verification{gone}))
	got, err = repo.Get(ctx, []string{poiID.String()})
	require.NoError(t, err)
	require.Equal(t, gone, got[poiID.String()])
	require.NoError(t, pool.QueryRow(ctx, `SELECT ST_Y(location) FROM points_of_interest WHERE id = $1`, poiID).Scan(&lat))
	require.InDelta(t, 38.7251, lat, 1e-9)

	empty, err := repo.Get(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
	require.NoError(t, repo.Save(ctx, nil))
}
