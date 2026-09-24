//go:build integration

package poi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func newSaveCity(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testDB.QueryRow(context.Background(),
		`INSERT INTO cities (name, country) VALUES ($1, 'Spain') RETURNING id`, "SaveTest "+uuid.NewString()).Scan(&id)
	require.NoError(t, err)
	return id
}

// A generated place is written with its position in the PostGIS column. The
// insert used to name latitude/longitude columns the table does not have, so
// every generated itinerary failed to save its places.
func TestSavePOItoPointsOfInterest_WritesLocation(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cityID := newSaveCity(t)

	id, err := repo.SavePOItoPointsOfInterest(ctx, locitypes.POIDetailedInfo{
		Name: "Royal Palace " + uuid.NewString(), Latitude: 40.418, Longitude: -3.714, Category: "Palace",
	}, cityID)
	require.NoError(t, err)

	var lat, lon float64
	require.NoError(t, testDB.QueryRow(ctx,
		`SELECT ST_Y(location), ST_X(location) FROM points_of_interest WHERE id = $1`, id).Scan(&lat, &lon))
	require.InDelta(t, 40.418, lat, 1e-6)
	require.InDelta(t, -3.714, lon, 1e-6)
}

// A place the model gave no position cannot be stored (location is NOT NULL)
// and must not be stored at null island either.
func TestSavePOItoPointsOfInterest_NoPositionIsRefused(t *testing.T) {
	repo := NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := repo.SavePOItoPointsOfInterest(context.Background(), locitypes.POIDetailedInfo{Name: "Nowhere " + uuid.NewString()}, newSaveCity(t))
	require.True(t, errors.Is(err, ErrPOIHasNoLocation), "err = %v", err)
}
