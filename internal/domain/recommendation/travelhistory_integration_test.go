//go:build integration

package recommendation

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/travelhistory"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

type recordingRecorder struct {
	mu     sync.Mutex
	visits []travelhistory.VisitInput
}

func (r *recordingRecorder) RecordVisit(_ context.Context, _ uuid.UUID, in travelhistory.VisitInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.visits = append(r.visits, in)
}

// A confirmed visit is only recorded when the POI resolves to a placed city.
// POIs with no city, and ids that do not exist, are skipped rather than guessed.
func TestRecordTravelHistoryResolvesOnlyPlacedPOIs(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()

	userID := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)",
		userID, "history-"+uuid.NewString()+"@loci.test")
	require.NoError(t, err)

	var cityID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO cities (name, country) VALUES ($1, $2) RETURNING id`,
		"Évora-"+uuid.NewString(), "Portugal").Scan(&cityID))

	var placedPOI, orphanPOI uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO points_of_interest (name, location, city_id)
		VALUES ($1, ST_SetSRID(ST_MakePoint($2, $3), 4326), $4) RETURNING id`,
		"Templo Romano", -7.9073, 38.5725, cityID).Scan(&placedPOI))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO points_of_interest (name, location)
		VALUES ($1, ST_SetSRID(ST_MakePoint($2, $3), 4326)) RETURNING id`,
		"Unplaced spot", -8.0, 38.0).Scan(&orphanPOI))

	rec := &recordingRecorder{}
	h := NewHandler(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).WithTravelHistory(rec)

	when := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	h.recordTravelHistory(ctx, userID, []visitCandidate{
		{poiID: orphanPOI.String(), occurredAt: when},
		{poiID: uuid.NewString(), occurredAt: when}, // does not exist
		{poiID: placedPOI.String(), occurredAt: when},
	})

	require.Len(t, rec.visits, 1)
	got := rec.visits[0]
	require.Equal(t, placedPOI.String(), got.POIID)
	require.Equal(t, "Templo Romano", got.POIName)
	require.NotNil(t, got.CityID)
	require.Equal(t, cityID, *got.CityID)
	require.Equal(t, "Portugal", got.Country)
	require.InDelta(t, 38.5725, got.Latitude, 1e-4)
	require.InDelta(t, -7.9073, got.Longitude, 1e-4)
	require.Equal(t, when, got.VisitedAt)
}
