//go:build integration

package poi

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// insertSearchablePOI seeds a row with the text columns the search_tsv generated
// column is built from (migration 0072).
func insertSearchablePOI(t *testing.T, cityID uuid.UUID, name, category, description string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Exec(context.Background(), `
		INSERT INTO points_of_interest (id, city_id, name, category, description, location)
		VALUES ($1, $2, $3, $4, $5, ST_SetSRID(ST_MakePoint(0, 0), 4326))`,
		id, cityID, name, category, description)
	require.NoError(t, err)
	return id
}

func lexicalRepo() *RepositoryImpl {
	return NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func hitNames(hits []LexicalHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.POI.Name
	}
	return out
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// The case the vector lane is worst at: a rare proper noun. Embeddings put
// "Casa Batlló" near every other Barcelona building; an inverted index does not.
func TestSearchPOIsLexical_FindsRareProperNoun(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Lexical City "+cityID.String()[:8])

	insertSearchablePOI(t, cityID, "Casa Batlló", "attraction", "A modernist building.")
	insertSearchablePOI(t, cityID, "Generic Tapas Bar", "restaurant", "Serves tapas.")
	insertSearchablePOI(t, cityID, "Another Museum", "attraction", "Has exhibits.")

	hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityID, "Casa Batlló", 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits, "exact proper-noun search returned nothing")

	require.Equal(t, "Casa Batlló", hits[0].POI.Name, "exact name did not rank first")
	require.True(t, hits[0].ExactName, "exact name match not flagged")
}

// Name matches must outrank description mentions — that is what the A/B/C
// weighting in search_tsv is for.
func TestSearchPOIsLexical_NameOutranksDescription(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Weight City "+cityID.String()[:8])

	insertSearchablePOI(t, cityID, "Seafood Palace", "restaurant", "Fresh fish daily.")
	insertSearchablePOI(t, cityID, "Hotel Rex", "hotel", "Close to the seafood market.")

	hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityID, "seafood", 10)
	require.NoError(t, err)
	require.Len(t, hits, 2, "expected both the name and description match")

	require.Equal(t, "Seafood Palace", hits[0].POI.Name,
		"description match outranked a name match; search_tsv weighting is wrong")
	require.Greater(t, hits[0].TextRank, hits[1].TextRank)
}

// The trigram arm. Tokenised search cannot match a misspelling; pg_trgm can.
func TestSearchPOIsLexical_MatchesMisspelling(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Typo City "+cityID.String()[:8])

	insertSearchablePOI(t, cityID, "Pastelaria Belem", "bakery", "Custard tarts.")

	hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityID, "Pastelaria Belm", 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits, "misspelled query found nothing; trigram arm is not working")
	require.Equal(t, "Pastelaria Belem", hits[0].POI.Name)
	require.Greater(t, hits[0].NameSimilarity, 0.3)
}

// User text is a bound parameter and reaches websearch_to_tsquery, which cannot
// raise a syntax error or let input restructure the query. Both properties
// matter: no error, and no rows.
func TestSearchPOIsLexical_HostileInputIsInert(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Hostile City "+cityID.String()[:8])
	insertSearchablePOI(t, cityID, "Quiet Cafe", "cafe", "Coffee.")

	hostile := []string{
		`foo") OR 1=1 --`,
		`'; DROP TABLE points_of_interest; --`,
		`) OR (`,
		`" AND "`,
		`!!!&&&|||`,
		`<-> NEAR NOT AND OR`,
	}

	for _, q := range hostile {
		hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityID, q, 10)
		require.NoErrorf(t, err, "hostile query errored instead of matching nothing: %q", q)
		require.Emptyf(t, hits, "hostile query matched rows: %q", q)
	}

	// The table is still there and still searchable.
	hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityID, "Quiet Cafe", 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
}

func TestSearchPOIsLexical_ScopesToCity(t *testing.T) {
	ctx := context.Background()
	cityA, cityB := uuid.New(), uuid.New()
	insertTestCity(t, cityA, "Scope A "+cityA.String()[:8])
	insertTestCity(t, cityB, "Scope B "+cityB.String()[:8])

	insertSearchablePOI(t, cityA, "Zzyzx Gallery", "attraction", "Art.")
	insertSearchablePOI(t, cityB, "Zzyzx Gallery", "attraction", "Art.")

	hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityA, "Zzyzx", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "search leaked across city boundaries")

	// uuid.Nil means "every city", used by surfaces that are not city-scoped.
	all, err := lexicalRepo().SearchPOIsLexical(ctx, uuid.Nil, "Zzyzx", 10)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestSearchPOIsLexical_EmptyQueryIsNotAnError(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Empty City "+cityID.String()[:8])
	insertSearchablePOI(t, cityID, "Somewhere", "cafe", "Coffee.")

	for _, q := range []string{"", "   ", "\t\n"} {
		hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityID, q, 10)
		require.NoErrorf(t, err, "empty query %q returned an error", q)
		require.Empty(t, hits)
	}
}

func TestSearchPOIsLexical_RejectsOversizedQuery(t *testing.T) {
	ctx := context.Background()
	long := make([]byte, maxLexicalQueryChars+1)
	for i := range long {
		long[i] = 'a'
	}

	_, err := lexicalRepo().SearchPOIsLexical(ctx, uuid.New(), string(long), 10)
	require.Error(t, err, "oversized query was not rejected")
}

func TestSearchPOIsLexical_RespectsLimit(t *testing.T) {
	ctx := context.Background()
	cityID := uuid.New()
	insertTestCity(t, cityID, "Limit City "+cityID.String()[:8])

	// Names must differ: migration 0068 makes (city_id, lower(btrim(name)))
	// unique, which is the POI identity guarantee.
	for _, suffix := range []string{"One", "Two", "Three", "Four", "Five"} {
		insertSearchablePOI(t, cityID, "Wanderlust Spot "+suffix, "attraction", "Nice.")
	}

	hits, err := lexicalRepo().SearchPOIsLexical(ctx, cityID, "wanderlust", 3)
	require.NoError(t, err)
	require.Len(t, hits, 3, "limit was not applied")
	require.True(t, contains(hitNames(hits), "Wanderlust Spot One"))
}
