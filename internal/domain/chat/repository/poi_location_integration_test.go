//go:build integration

package repository

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/db"
)

// GetOrCreatePOI handed ST_MakePoint (x=longitude, y=latitude) the latitude
// first, so every POI it created was stored mirrored across the diagonal:
// Vieux Nice (43.698, 7.276) came back as lat 7.276, lon 43.698, a pin in
// East Africa. The row's first location also wins every later upsert, so the
// error never healed on its own.
func TestGetOrCreatePOI_StoresLongitudeAsX(t *testing.T) {
	testsupport.Truncate(t, testChatDB, "points_of_interest", "cities")
	ctx := context.Background()

	cityID := uuid.New()
	if _, err := testChatDB.Exec(ctx,
		"INSERT INTO cities (id, name, country) VALUES ($1, 'Nice', 'France')", cityID); err != nil {
		t.Fatalf("insert city: %v", err)
	}

	repo := NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var poiID uuid.UUID
	if err := db.WithTx(ctx, testChatDB, func(tx pgx.Tx) error {
		var err error
		poiID, err = repo.GetOrCreatePOI(ctx, tx, locitypes.POIDetailedInfo{
			Name: "Vieux Nice", Latitude: 43.698, Longitude: 7.276, Category: "Landmark",
		}, cityID, uuid.New())
		return err
	}); err != nil {
		t.Fatalf("GetOrCreatePOI: %v", err)
	}

	lat, lon := storedLatLon(t, poiID)
	if lat != 43.698 || lon != 7.276 {
		t.Fatalf("stored lat=%v lon=%v, want lat=43.698 lon=7.276", lat, lon)
	}
}

// The repair migration swaps back rows that sit far from their city but
// would sit near it mirrored, and leaves everything else alone.
func TestRepairSwappedPOILocations(t *testing.T) {
	testsupport.Truncate(t, testChatDB, "points_of_interest", "cities")
	ctx := context.Background()

	nice := uuid.New()
	noCenter := uuid.New()
	if _, err := testChatDB.Exec(ctx, `
		INSERT INTO cities (id, name, country, center_location) VALUES
		  ($1, 'Nice', 'France', ST_SetSRID(ST_MakePoint(7.262, 43.710), 4326)),
		  ($2, 'Nowhere', 'France', NULL)`, nice, noCenter); err != nil {
		t.Fatalf("insert cities: %v", err)
	}

	insert := func(name string, city uuid.UUID, x, y float64) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := testChatDB.QueryRow(ctx, `
			INSERT INTO points_of_interest (name, city_id, location)
			VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)) RETURNING id`,
			name, city, x, y).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		return id
	}
	swapped := insert("Vieux Nice", nice, 43.698, 7.276)    // mirrored
	correct := insert("Castle Hill", nice, 7.280, 43.695)   // already right
	unknown := insert("Somewhere", noCenter, 43.698, 7.276) // no center: can't tell

	up, err := os.ReadFile("../../../../pkg/db/migrations/0099_repair_swapped_poi_locations.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	stmt := string(up)
	stmt = stmt[strings.Index(stmt, "-- +goose StatementBegin")+len("-- +goose StatementBegin"):]
	stmt = stmt[:strings.Index(stmt, "-- +goose StatementEnd")]
	if _, err := testChatDB.Exec(ctx, stmt); err != nil {
		t.Fatalf("run repair: %v", err)
	}

	for _, c := range []struct {
		name     string
		id       uuid.UUID
		lat, lon float64
	}{
		{"swapped row is repaired", swapped, 43.698, 7.276},
		{"correct row is untouched", correct, 43.695, 7.280},
		{"row without a city center is untouched", unknown, 7.276, 43.698},
	} {
		lat, lon := storedLatLon(t, c.id)
		if lat != c.lat || lon != c.lon {
			t.Errorf("%s: got lat=%v lon=%v, want lat=%v lon=%v", c.name, lat, lon, c.lat, c.lon)
		}
	}
}

func storedLatLon(t *testing.T, id uuid.UUID) (lat, lon float64) {
	t.Helper()
	if err := testChatDB.QueryRow(context.Background(),
		"SELECT ST_Y(location), ST_X(location) FROM points_of_interest WHERE id = $1", id,
	).Scan(&lat, &lon); err != nil {
		t.Fatalf("read location: %v", err)
	}
	return lat, lon
}
