//go:build integration

package userdata

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func exporter() *Exporter {
	return NewExporter(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func seedUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(),
		`INSERT INTO users (id, email) VALUES ($1, $2)`, id, "export-"+id.String()+"@example.com")
	require.NoError(t, err)
	return id
}

// Every section must be present in the output even when empty, so a user can
// tell "you hold none of these" from "this was left out".
func TestBuildIncludesEverySectionForAnEmptyAccount(t *testing.T) {
	ctx := context.Background()
	userID := seedUser(t)

	bundle, err := exporter().Build(ctx, userID, map[string]any{"email": "x@example.com"})
	require.NoError(t, err)
	require.Empty(t, bundle.Incomplete, "sections failed to read: %v", bundle.Incomplete)

	raw, err := bundle.JSON()
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	for _, key := range []string{
		"trips", "lists", "favorites", "saved_itineraries",
		"visited_cities", "visited_pois", "chat_sessions",
		"taste_traits", "taste_evidence", "api_keys",
	} {
		require.Contains(t, decoded, key, "export omits the %q section entirely", key)
	}
	require.Equal(t, userID.String(), decoded["user_id"])
}

func TestBuildCarriesRealData(t *testing.T) {
	ctx := context.Background()
	userID := seedUser(t)

	_, err := testDB.Exec(ctx, `
		INSERT INTO user_favorites (user_id, item_id, item_name, content_type, city_name)
		VALUES ($1, $2, 'Bar Alta', 'poi', 'Lisbon')`, userID, uuid.NewString())
	require.NoError(t, err)

	_, err = testDB.Exec(ctx, `
		INSERT INTO user_taste_traits (user_id, trait_key, label, score, confidence, evidence_count)
		VALUES ($1, 'bar', 'Bars', 0.7, 0.5, 3)`, userID)
	require.NoError(t, err)

	bundle, err := exporter().Build(ctx, userID, nil)
	require.NoError(t, err)

	favorites, ok := bundle.Favorites.([]map[string]any)
	require.True(t, ok, "favorites came back as %T", bundle.Favorites)
	require.Len(t, favorites, 1)
	require.Equal(t, "Bar Alta", favorites[0]["item_name"])

	traits, ok := bundle.TasteTraits.([]map[string]any)
	require.True(t, ok)
	require.Len(t, traits, 1)
	require.Equal(t, "bar", traits[0]["trait_key"])
}

// A credential-equivalent value must never land in a file users email around.
func TestBuildNeverExportsKeyMaterial(t *testing.T) {
	ctx := context.Background()
	userID := seedUser(t)

	_, err := testDB.Exec(ctx, `
		INSERT INTO api_keys (user_id, name, key_prefix, key_hash)
		VALUES ($1, 'exported key', 'loci_sk_exp', '\x0badc0de'::bytea)`, userID)
	require.NoError(t, err)

	bundle, err := exporter().Build(ctx, userID, nil)
	require.NoError(t, err)

	keys, ok := bundle.APIKeys.([]map[string]any)
	require.True(t, ok)
	require.Len(t, keys, 1)

	require.Equal(t, "exported key", keys[0]["name"])
	require.NotContains(t, keys[0], "key_hash", "the export leaked API key material")

	raw, err := bundle.JSON()
	require.NoError(t, err)
	require.NotContains(t, string(raw), "badc0de", "key hash appears in the serialized export")
}

// Scoping: one account's export must never contain another's rows.
func TestBuildIsScopedToOneUser(t *testing.T) {
	ctx := context.Background()
	mine := seedUser(t)
	theirs := seedUser(t)

	_, err := testDB.Exec(ctx, `
		INSERT INTO user_favorites (user_id, item_id, item_name, content_type, city_name)
		VALUES ($1, $2, 'Their Secret Bar', 'poi', 'Porto')`, theirs, uuid.NewString())
	require.NoError(t, err)

	bundle, err := exporter().Build(ctx, mine, nil)
	require.NoError(t, err)

	raw, err := bundle.JSON()
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Their Secret Bar")
	require.NotContains(t, string(raw), theirs.String())
}

func TestBundleJSONIsValidAndIndented(t *testing.T) {
	ctx := context.Background()
	bundle, err := exporter().Build(ctx, seedUser(t), nil)
	require.NoError(t, err)

	raw, err := bundle.JSON()
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	require.Contains(t, string(raw), "\n  ", "export is not human-readable")
}
