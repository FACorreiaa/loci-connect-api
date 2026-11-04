//go:build integration

package repository

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Places are unique per city by lower(btrim(name)). The lookup used to match the
// raw name, so the same place spelled with different case or padding missed it,
// hit the unique index on insert, and aborted the transaction that saves the
// whole answer — interaction, itinerary and all.
func TestGetOrCreatePOI_MatchesTheUniqueIndex(t *testing.T) {
	ctx := context.Background()
	repo := NewRepositoryImpl(testChatDB, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var cityID uuid.UUID
	if err := testChatDB.QueryRow(ctx,
		`INSERT INTO cities (name, country) VALUES ($1, 'France') RETURNING id`,
		"GetOrCreate "+uuid.NewString()).Scan(&cityID); err != nil {
		t.Fatalf("insert city: %v", err)
	}
	name := "Louvre " + uuid.NewString()[:8]
	poi := func(n string) locitypes.POIDetailedInfo {
		return locitypes.POIDetailedInfo{Name: n, Latitude: 48.8606, Longitude: 2.3376, Category: "Museum"}
	}

	tx, err := testChatDB.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	first, err := repo.GetOrCreatePOI(ctx, tx, poi(name), cityID, uuid.Nil)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := repo.GetOrCreatePOI(ctx, tx, poi("  "+upper(name)+" "), cityID, uuid.Nil)
	if err != nil {
		t.Fatalf("same place, other spelling: %v", err)
	}
	if first != second {
		t.Fatalf("same place got two ids: %s and %s", first, second)
	}
	// The transaction must still be usable afterwards.
	if _, err := tx.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatalf("transaction aborted: %v", err)
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
