//go:build integration

package city

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/geocode"
	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// This is the bug, reproduced against a real database and then fixed.
//
// /compare answered a request for Porto with
// "400 compare: origin city not found: Porto", because the cities table only
// ever held cities some earlier LLM conversation had generated content for, and
// resolution was a single fuzzy query against it. Everything else is covered by
// unit tests with fakes; what those cannot show is that the SQL, the PostGIS
// columns, the trigram index and the persistence actually agree with each other.
//
// The geocoder is real too, behind LOCI_LIVE_GEOCODE=1, because a stub here
// would only be re-testing the fake.
func TestResolver_FindsACityTheDatabaseHasNeverHeardOf(t *testing.T) {
	if os.Getenv("LOCI_LIVE_GEOCODE") != "1" {
		t.Skip("set LOCI_LIVE_GEOCODE=1 to run the resolver against the real geocoder")
	}

	ctx := context.Background()
	// The condition that produced the bug: no row for the city being asked for.
	_, err := testCityDB.Exec(ctx, "DELETE FROM cities WHERE name IN ('Porto', 'Évora', 'Beja')")
	require.NoError(t, err)

	resolver := NewResolver(
		testCityRepo,
		geocode.NewOpenMeteo("", "", httpx.New(httpx.Config{}), nil),
		testLogger(),
	)

	t.Run("resolves and persists a city with no row", func(t *testing.T) {
		got, err := resolver.Resolve(ctx, ResolveQuery{Name: "Porto"})
		require.NoError(t, err, "this is the call that used to fail the request")

		assert.Equal(t, ResolvedFromGeocoder, got.Source)
		assert.True(t, got.Created, "a city with no row should have been created")
		assert.NotEqual(t, uuid.Nil, got.City.ID)
		assert.InDelta(t, 41.15, got.Lat, 1.0)
		assert.InDelta(t, -8.61, got.Lon, 1.0)
		// Not the literal "Unknown" the older create-on-demand paths write.
		assert.Equal(t, "Portugal", got.City.Country)

		// The row is really there, with a real geometry — a NULL
		// center_location would resolve today and fail tomorrow.
		var country string
		var lat, lon float64
		err = testCityDB.QueryRow(ctx, `
			SELECT country, ST_Y(center_location), ST_X(center_location)
			FROM cities WHERE id = $1`, got.City.ID).Scan(&country, &lat, &lon)
		require.NoError(t, err)
		assert.Equal(t, "Portugal", country)
		assert.InDelta(t, 41.15, lat, 1.0)
		assert.InDelta(t, -8.61, lon, 1.0)
	})

	t.Run("answers from the database the second time", func(t *testing.T) {
		// Same query, but with no geocoder at all. If this still resolves, the
		// first call really did persist something usable — which is what stops
		// every comparison costing an outbound request.
		offline := NewResolver(testCityRepo, nil, testLogger())

		got, err := offline.Resolve(ctx, ResolveQuery{Name: "Porto"})
		require.NoError(t, err)
		assert.Equal(t, ResolvedFromDB, got.Source)
		assert.False(t, got.Created)
	})

	t.Run("an accented query and an unaccented one reach the same city", func(t *testing.T) {
		accented, err := resolver.Resolve(ctx, ResolveQuery{Name: "Évora"})
		require.NoError(t, err)
		// Deliberately not asserting the spelling: Open-Meteo's canonical name
		// for this city is the unaccented "Evora", and persisting the
		// provider's spelling rather than the user's is the point. What matters
		// is that it is the right place.
		assert.InDelta(t, 38.57, accented.Lat, 0.5)
		assert.InDelta(t, -7.90, accented.Lon, 0.5)

		// LOWER('Evora') never equals 'Évora', so a query that differs only by
		// accent has to find the row through the fold rather than the geocoder.
		offline := NewResolver(testCityRepo, nil, testLogger())
		plain, err := offline.Resolve(ctx, ResolveQuery{Name: "Evora"})
		require.NoError(t, err)
		assert.Equal(t, accented.City.ID, plain.City.ID)
		assert.Equal(t, ResolvedFromDB, plain.Source)
	})

	t.Run("a name that is not a place is refused, not invented", func(t *testing.T) {
		_, err := resolver.Resolve(ctx, ResolveQuery{Name: "Zzzqqxwvu"})
		require.ErrorIs(t, err, ErrCityUnresolvable)

		var count int
		require.NoError(t, testCityDB.QueryRow(ctx,
			"SELECT COUNT(*) FROM cities WHERE name = 'Zzzqqxwvu'").Scan(&count))
		assert.Zero(t, count, "a failed resolve must not leave a row behind")
	})
}

// A stub row with no coordinates is what the chat stream writes when the model
// names a city but returns no city_data. It used to make compare fail with
// "origin city missing coordinates"; it should now be healed in place.
func TestResolver_BackfillsACoordinatelessStubRow(t *testing.T) {
	if os.Getenv("LOCI_LIVE_GEOCODE") != "1" {
		t.Skip("set LOCI_LIVE_GEOCODE=1 to run the resolver against the real geocoder")
	}

	ctx := context.Background()
	_, err := testCityDB.Exec(ctx, "DELETE FROM cities WHERE name = 'Beja'")
	require.NoError(t, err)

	var stubID uuid.UUID
	require.NoError(t, testCityDB.QueryRow(ctx, `
		INSERT INTO cities (name, country, state_province)
		VALUES ('Beja', 'Unknown', 'Unknown') RETURNING id`).Scan(&stubID))

	resolver := NewResolver(
		testCityRepo,
		geocode.NewOpenMeteo("", "", httpx.New(httpx.Config{}), nil),
		testLogger(),
	)

	// Biased from Porto, exactly as a comparison does. Without the bias the
	// geocoder's best "Beja" is Béja in Tunisia, which is larger and 1,900 km
	// away.
	got, err := resolver.Resolve(ctx, ResolveQuery{Name: "Beja", NearLat: 41.15, NearLon: -8.61})
	require.NoError(t, err)

	// The same row, not a corrected sibling: anything already pointing at the
	// stub — POIs, itineraries — has to keep pointing at something useful.
	assert.Equal(t, stubID, got.City.ID)

	var rows int
	require.NoError(t, testCityDB.QueryRow(ctx,
		"SELECT COUNT(*) FROM cities WHERE name = 'Beja'").Scan(&rows))
	assert.Equal(t, 1, rows, "backfilling must not insert a second Beja")

	var lat, lon float64
	require.NoError(t, testCityDB.QueryRow(ctx, `
		SELECT ST_Y(center_location), ST_X(center_location)
		FROM cities WHERE id = $1`, stubID).Scan(&lat, &lon))
	assert.InDelta(t, 38.0, lat, 1.0)
	assert.InDelta(t, -7.86, lon, 1.0)
}

// FindCityNear is what attaches a city — and therefore its places — to a request
// that supplied raw coordinates. It needs real PostGIS to mean anything.
func TestResolver_AttachesANearbyCityToSuppliedCoordinates(t *testing.T) {
	ctx := context.Background()
	_, err := testCityDB.Exec(ctx, "DELETE FROM cities WHERE name = 'NearTestCity'")
	require.NoError(t, err)

	lat, lon := 41.14961, -8.61099
	var id uuid.UUID
	require.NoError(t, testCityDB.QueryRow(ctx, `
		INSERT INTO cities (name, country, state_province, center_location)
		VALUES ('NearTestCity', 'Portugal', 'Porto',
		        ST_SetSRID(ST_MakePoint($1, $2), 4326))
		RETURNING id`, lon, lat).Scan(&id))

	resolver := NewResolver(testCityRepo, nil, testLogger())

	// A few kilometres away, well inside the radius.
	got, err := resolver.Resolve(ctx, ResolveQuery{Lat: lat + 0.02, Lon: lon + 0.02})
	require.NoError(t, err)
	assert.Equal(t, ResolvedFromClient, got.Source)
	assert.Equal(t, id, got.City.ID, "a nearby city should have been attached")

	// The far side of the planet must not attach anything — the unbounded
	// nearest-city query would happily have returned this row.
	far, err := resolver.Resolve(ctx, ResolveQuery{Lat: 35.68, Lon: 139.69})
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, far.City.ID, "Tokyo is not near Porto")
}
