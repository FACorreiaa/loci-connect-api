//go:build integration

package placeintel

import (
	"context"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// These exercise the corroboration rules against a real Postgres, because they
// are expressed in SQL — an upsert conflict clause and a COUNT(DISTINCT) — and
// a unit test with a fake would only assert that the fake behaves as written.
//
// Run with: PLACEINTEL_TEST_DSN=postgres://... go test -tags=integration ./internal/domain/placeintel/

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PLACEINTEL_TEST_DSN")
	if dsn == "" {
		t.Skip("PLACEINTEL_TEST_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func ctxAs(userID uuid.UUID) context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, userID.String())
}

// seedScout creates a user and returns its id, cleaning up afterwards.
func seedScout(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, username, email, password_hash)
		VALUES ($1, $2, $3, 'x')`, id, "scout-"+id.String()[:8], id.String()+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// seedPlace creates a city and a POI in it.
func seedPlace(t *testing.T, pool *pgxpool.Pool) (cityID, poiID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	cityID = uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO cities (id, name, country, center_location)
		VALUES ($1, $2, 'Testland', ST_SetSRID(ST_MakePoint(0, 0), 4326))`,
		cityID, "Testville-"+cityID.String()[:8])
	require.NoError(t, err)

	poiID = uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO points_of_interest (id, name, location, city_id)
		VALUES ($1, 'Test Cafe', ST_SetSRID(ST_MakePoint(0, 0), 4326), $2)`, poiID, cityID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM place_facts WHERE poi_id = $1`, poiID.String())
		_, _ = pool.Exec(ctx, `DELETE FROM place_claims WHERE poi_id = $1`, poiID.String())
		_, _ = pool.Exec(ctx, `DELETE FROM points_of_interest WHERE id = $1`, poiID)
		_, _ = pool.Exec(ctx, `DELETE FROM cities WHERE id = $1`, cityID)
	})
	return cityID, poiID
}

func submit(t *testing.T, h *Handler, scout, poiID uuid.UUID, field placev1.PlaceFactField, value string) *placev1.SubmitPlaceClaimResponse {
	t.Helper()
	response, err := h.SubmitPlaceClaim(ctxAs(scout), connect.NewRequest(&placev1.SubmitPlaceClaimRequest{
		ClientClaimId: uuid.NewString(),
		PoiId:         poiID.String(),
		Field:         field,
		Value:         value,
		ObservedAt:    timestamppb.New(time.Now()),
	}))
	require.NoError(t, err)
	return response.Msg
}

// The headline behaviour: one scout's report is recorded but not yet true; a
// second scout saying the same thing makes it true, and both are credited.
func TestClaimBecomesFactOnSecondScout(t *testing.T) {
	pool := testPool(t)
	handler := NewHandler(pool, nil)
	_, poiID := seedPlace(t, pool)
	alice, bob := seedScout(t, pool), seedScout(t, pool)
	ctx := context.Background()

	first := submit(t, handler, alice, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, "busy")
	assert.Equal(t, placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_PENDING, first.Status)

	var confidence float64
	var contributors int32
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT confidence, contributor_count FROM place_facts WHERE poi_id = $1 AND field = 'crowd_level'`,
		poiID.String()).Scan(&confidence, &contributors))
	assert.InDelta(t, pendingFactConfidence, confidence, 0.001, "a lone report is low confidence")
	assert.Equal(t, int32(1), contributors)

	second := submit(t, handler, bob, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, "busy")
	assert.Equal(t, placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_ACCEPTED, second.Status)

	require.NoError(t, pool.QueryRow(ctx, `
		SELECT confidence, contributor_count FROM place_facts WHERE poi_id = $1 AND field = 'crowd_level'`,
		poiID.String()).Scan(&confidence, &contributors))
	assert.InDelta(t, 0.6, confidence, 0.001)
	assert.Equal(t, int32(2), contributors)

	// Both scouts earned it, not just whoever happened to submit last.
	for _, scout := range []uuid.UUID{alice, bob} {
		var accepted, reputation int32
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT accepted_claims, reputation FROM contributor_profiles WHERE user_id = $1`, scout).
			Scan(&accepted, &reputation))
		assert.Equal(t, int32(1), accepted, "scout %s was not credited", scout)
		assert.Equal(t, int32(3), reputation)
	}
}

// A later lone dissenter must not be able to overwrite what two scouts agreed.
func TestVerifiedFactIsNotDowngradedByOneDissenter(t *testing.T) {
	pool := testPool(t)
	handler := NewHandler(pool, nil)
	_, poiID := seedPlace(t, pool)
	alice, bob, carol := seedScout(t, pool), seedScout(t, pool), seedScout(t, pool)

	submit(t, handler, alice, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL, "quiet")
	submit(t, handler, bob, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL, "quiet")
	submit(t, handler, carol, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL, "loud")

	var value string
	var contributors int32
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT value, contributor_count FROM place_facts WHERE poi_id = $1 AND field = 'noise_level'`,
		poiID.String()).Scan(&value, &contributors))
	assert.Equal(t, "quiet", value)
	assert.Equal(t, int32(2), contributors)
}

// Two scouts who pick the same set in a different order must corroborate.
func TestMultiSelectOrderDoesNotPreventCorroboration(t *testing.T) {
	pool := testPool(t)
	handler := NewHandler(pool, nil)
	_, poiID := seedPlace(t, pool)
	alice, bob := seedScout(t, pool), seedScout(t, pool)

	submit(t, handler, alice, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY, "vegan,gluten_free")
	second := submit(t, handler, bob, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_DIETARY, "gluten_free,vegan")

	assert.Equal(t, placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_ACCEPTED, second.Status,
		"the same set written two ways must be one claim")
}

func TestSubmitRejectsValuesOutsideTheVocabulary(t *testing.T) {
	pool := testPool(t)
	handler := NewHandler(pool, nil)
	_, poiID := seedPlace(t, pool)
	alice := seedScout(t, pool)

	_, err := handler.SubmitPlaceClaim(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceClaimRequest{
		ClientClaimId: uuid.NewString(),
		PoiId:         poiID.String(),
		Field:         placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL,
		Value:         "absolutely rammed mate",
		ObservedAt:    timestamppb.New(time.Now()),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestSubmitRejectsUnknownPlace(t *testing.T) {
	pool := testPool(t)
	handler := NewHandler(pool, nil)
	alice := seedScout(t, pool)

	_, err := handler.SubmitPlaceClaim(ctxAs(alice), connect.NewRequest(&placev1.SubmitPlaceClaimRequest{
		ClientClaimId: uuid.NewString(),
		PoiId:         uuid.NewString(),
		Field:         placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE,
		Value:         "cosy",
		ObservedAt:    timestamppb.New(time.Now()),
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// Tasks must follow the scout: a place in a city they have been browsing, asking
// only what is not already known.
func TestVerificationTasksFollowRecentInteractions(t *testing.T) {
	pool := testPool(t)
	handler := NewHandler(pool, nil)
	_, poiID := seedPlace(t, pool)
	alice := seedScout(t, pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO poi_interactions
			(id, user_id, poi_id, poi_name, poi_category, interaction_type,
			 user_latitude, user_longitude, poi_latitude, poi_longitude, distance)
		VALUES ($1, $2, $3, 'Test Cafe', 'cafe', 'view', 0, 0, 0, 0, 0)`,
		uuid.New(), alice, poiID.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM poi_interactions WHERE user_id = $1`, alice)
	})

	response, err := handler.ListVerificationTasks(ctxAs(alice),
		connect.NewRequest(&placev1.ListVerificationTasksRequest{Limit: 20}))
	require.NoError(t, err)

	var found *placev1.VerificationTask
	for _, task := range response.Msg.GetTasks() {
		if task.GetPoiId() == poiID.String() {
			found = task
		}
	}
	require.NotNil(t, found, "the place the scout just looked at should be offered")
	assert.ElementsMatch(t, contributableFields, found.GetRequestedFields(),
		"a place with no facts should be asked about everything")

	// Once a fact is known, that question stops being asked.
	submit(t, handler, alice, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE, "cosy")
	response, err = handler.ListVerificationTasks(ctxAs(alice),
		connect.NewRequest(&placev1.ListVerificationTasksRequest{Limit: 20}))
	require.NoError(t, err)
	for _, task := range response.Msg.GetTasks() {
		if task.GetPoiId() == poiID.String() {
			assert.NotContains(t, task.GetRequestedFields(),
				placev1.PlaceFactField_PLACE_FACT_FIELD_VIBE)
		}
	}
}
