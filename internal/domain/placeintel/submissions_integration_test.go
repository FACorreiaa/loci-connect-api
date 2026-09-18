//go:build integration

package placeintel

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The submitter is the first voice, so a fresh submission needs exactly one
// more person.
func TestConfirmationsNeeded(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int32(1), confirmationsNeeded(0), "submitter counts as one")
	assert.Equal(t, int32(0), confirmationsNeeded(1), "one confirmation is enough")
	assert.Equal(t, int32(0), confirmationsNeeded(5), "never negative")
}

func TestSubmitPlaceIsPendingAndInvisible(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice := seedScout(t, pool)

	response, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Tasca do Fernando",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)
	assert.Equal(t, placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_PENDING, response.Msg.GetStatus())
	assert.Equal(t, int32(1), response.Msg.GetConfirmationsNeeded())

	// The safety property: nothing the generator reads has been touched.
	var pois int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::int FROM points_of_interest WHERE lower(btrim(name)) = 'tasca do fernando'`).Scan(&pois))
	assert.Equal(t, 0, pois, "a pending place must not be in points_of_interest")
}

func TestSubmitPlaceIsIdempotent(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice := seedScout(t, pool)
	clientID := uuid.NewString()

	req := func() *connect.Request[placev1.SubmitPlaceRequest] {
		return connect.NewRequest(&placev1.SubmitPlaceRequest{
			ClientSubmissionId: clientID,
			Name:               "Padaria Nova",
			CityName:           cityNameFor(t, pool, cityID),
		})
	}
	first, err := handler.SubmitPlace(ctxAs(alice), req())
	require.NoError(t, err)
	second, err := handler.SubmitPlace(ctxAs(alice), req())
	require.NoError(t, err)
	assert.Equal(t, first.Msg.GetSubmissionId(), second.Msg.GetSubmissionId(), "a retry must not create a second row")
}

func TestSubmitPlaceRejectsOneAlreadyOnTheGuide(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID, poiID := seedPlaceInCity(t, pool)
	_ = poiID
	alice := seedScout(t, pool)

	_, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "  test cafe  ", // same place, sloppier typing
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
}

// newSubmissionHandler builds a Handler with the city resolver stubbed to a
// city that already exists, because resolving a city is not what these tests
// are about.
func newSubmissionHandler(t *testing.T, pool *pgxpool.Pool) *Handler {
	t.Helper()
	h := NewHandler(pool, nil)
	h.cities = stubResolver{pool: pool}
	h.pois = stubUpserter{pool: pool}
	return h
}

type stubResolver struct{ pool *pgxpool.Pool }

func (s stubResolver) ResolveCity(ctx context.Context, name, country string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var found string
	err := s.pool.QueryRow(ctx, `SELECT id, name FROM cities WHERE name = $1`, name).Scan(&id, &found)
	return id, found, err
}

func seedCity(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO cities (id, name, country, center_location)
		VALUES ($1, $2, 'Testland', ST_SetSRID(ST_MakePoint(0, 0), 4326))`,
		id, "Testville-"+id.String()[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM cities WHERE id = $1`, id)
	})
	return id
}

// seedCityAt is a sibling of seedCity for tests that need a city with a real,
// non-zero centre — seedCity itself is pinned to (0, 0), which is exactly the
// value the promotion adapter treats as "no coordinates", so it cannot be
// reused to prove a fallback to the city's centre actually happened.
func seedCityAt(t *testing.T, pool *pgxpool.Pool, lat, lng float64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO cities (id, name, country, center_location)
		VALUES ($1, $2, 'Testland', ST_SetSRID(ST_MakePoint($3, $4), 4326))`,
		id, "Testville-"+id.String()[:8], lng, lat)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM cities WHERE id = $1`, id)
	})
	return id
}

func cityNameFor(t *testing.T, pool *pgxpool.Pool, cityID uuid.UUID) string {
	t.Helper()
	var name string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT name FROM cities WHERE id = $1`, cityID).Scan(&name))
	return name
}

// seedPlaceInCity creates a city and a POI named "Test Cafe" inside it.
func seedPlaceInCity(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	cityID := seedCity(t, pool)
	poiID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO points_of_interest (id, name, location, city_id)
		VALUES ($1, 'Test Cafe', ST_SetSRID(ST_MakePoint(0, 0), 4326), $2)`, poiID, cityID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM points_of_interest WHERE id = $1`, poiID)
	})
	return cityID, poiID
}

func TestSecondPersonPromotesThePlace(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice, bob := seedScout(t, pool), seedScout(t, pool)

	submitted, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Miradouro Novo",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)

	confirmed, err := handler.ConfirmPlace(ctxAs(bob), connect.NewRequest(&placev1.ConfirmPlaceRequest{
		SubmissionId: submitted.Msg.GetSubmissionId(),
	}))
	require.NoError(t, err)

	assert.Equal(t, placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_ACCEPTED, confirmed.Msg.GetStatus())
	assert.Equal(t, int32(0), confirmed.Msg.GetConfirmationsNeeded())
	require.NotEmpty(t, confirmed.Msg.GetPoiId(), "promotion must return the new place")

	// Now — and only now — it exists where the generator can find it.
	var pois int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT COUNT(*)::int FROM points_of_interest WHERE id = $1`,
		uuid.MustParse(confirmed.Msg.GetPoiId())).Scan(&pois))
	assert.Equal(t, 1, pois)

	// Both people are credited, not just whoever acted last.
	for _, scout := range []uuid.UUID{alice, bob} {
		var accepted int32
		require.NoError(t, pool.QueryRow(context.Background(), `
			SELECT accepted_claims FROM contributor_profiles WHERE user_id = $1`, scout).Scan(&accepted))
		assert.Equal(t, int32(1), accepted, "scout %s was not credited", scout)
	}
}

// A submission with no coordinates of its own must inherit its city's centre
// at promotion time, not fall through to (0, 0) — Null Island — which the
// promotion adapter refuses. seedCity's city sits at (0, 0) itself, so this
// needs a city with a real centre for the assertion to mean anything.
func TestConfirmPlaceFallsBackToCityCentreWhenSubmissionHasNoCoordinates(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	const cityLat, cityLng = 38.7223, -9.1393 // Lisbon
	cityID := seedCityAt(t, pool, cityLat, cityLng)
	alice, bob := seedScout(t, pool), seedScout(t, pool)

	submitted, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Elevador Novo",
		CityName:           cityNameFor(t, pool, cityID),
		// Latitude/Longitude deliberately omitted.
	}))
	require.NoError(t, err)

	confirmed, err := handler.ConfirmPlace(ctxAs(bob), connect.NewRequest(&placev1.ConfirmPlaceRequest{
		SubmissionId: submitted.Msg.GetSubmissionId(),
	}))
	require.NoError(t, err)

	assert.Equal(t, placev1.PlaceSubmissionStatus_PLACE_SUBMISSION_STATUS_ACCEPTED, confirmed.Msg.GetStatus())
	require.NotEmpty(t, confirmed.Msg.GetPoiId(), "promotion must succeed by inheriting the city's centre")

	var poiLat, poiLng float64
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT ST_Y(location::geometry), ST_X(location::geometry)
		FROM points_of_interest WHERE id = $1`,
		uuid.MustParse(confirmed.Msg.GetPoiId())).Scan(&poiLat, &poiLng))
	assert.InDelta(t, cityLat, poiLat, 0.0001, "promoted place must carry the city's latitude, not 0")
	assert.InDelta(t, cityLng, poiLng, 0.0001, "promoted place must carry the city's longitude, not 0")
}

func TestConfirmingYourOwnSubmissionFails(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice := seedScout(t, pool)

	submitted, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Solo Bar",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)

	_, err = handler.ConfirmPlace(ctxAs(alice), connect.NewRequest(&placev1.ConfirmPlaceRequest{
		SubmissionId: submitted.Msg.GetSubmissionId(),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err),
		"corroboration has to come from somebody else")
}

// The feed is for corroboration, so it must never hand somebody their own
// submission, and it must hand it to everybody else.
func TestPendingPlacesExcludeYourOwn(t *testing.T) {
	pool := testPool(t)
	handler := newSubmissionHandler(t, pool)
	cityID := seedCity(t, pool)
	alice, bob := seedScout(t, pool), seedScout(t, pool)

	_, err := handler.SubmitPlace(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceRequest{
		ClientSubmissionId: uuid.NewString(),
		Name:               "Quiosque do Parque",
		CityName:           cityNameFor(t, pool, cityID),
	}))
	require.NoError(t, err)

	mine, err := handler.ListPendingPlaces(ctxAs(alice),
		connect.NewRequest(&placev1.ListPendingPlacesRequest{Limit: 20}))
	require.NoError(t, err)
	for _, p := range mine.Msg.GetPlaces() {
		assert.NotEqual(t, "Quiosque do Parque", p.GetName(), "you cannot confirm your own place")
	}

	theirs, err := handler.ListPendingPlaces(ctxAs(bob),
		connect.NewRequest(&placev1.ListPendingPlacesRequest{Limit: 20}))
	require.NoError(t, err)
	found := false
	for _, p := range theirs.Msg.GetPlaces() {
		if p.GetName() == "Quiosque do Parque" {
			found = true
			assert.Equal(t, int32(1), p.GetConfirmationsNeeded())
		}
	}
	assert.True(t, found, "somebody else should be asked to confirm it")
}

type stubUpserter struct{ pool *pgxpool.Pool }

func (s stubUpserter) UpsertPOIByIdentity(ctx context.Context, name string, cityID uuid.UUID, lat, lng float64) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO points_of_interest (id, name, location, city_id)
		VALUES (gen_random_uuid(), $1, ST_SetSRID(ST_MakePoint($2, $3), 4326), $4)
		ON CONFLICT (city_id, lower(btrim(name))) DO UPDATE SET updated_at = NOW()
		RETURNING id`, name, lng, lat, cityID).Scan(&id)
	return id, err
}
