//go:build integration

package memory

import (
	"context"
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

type fixture struct {
	userID  uuid.UUID
	cityID  uuid.UUID
	poiID   uuid.UUID
	traitID string
}

// seed builds a user with feedback on a categorised POI, the derived trait, and
// the evidence linking them — the shape the reranker produces.
func seed(t *testing.T, category string, events ...string) fixture {
	t.Helper()
	ctx := context.Background()

	f := fixture{userID: uuid.New(), cityID: uuid.New(), poiID: uuid.New(), traitID: category}

	_, err := testDB.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`,
		f.userID, "memory-"+f.userID.String()+"@example.com")
	require.NoError(t, err)

	_, err = testDB.Exec(ctx, `INSERT INTO cities (id, name, country) VALUES ($1, $2, 'Testland')`,
		f.cityID, "Memory City "+f.cityID.String()[:8])
	require.NoError(t, err)

	_, err = testDB.Exec(ctx, `
		INSERT INTO points_of_interest (id, city_id, name, category, location)
		VALUES ($1, $2, $3, $4, ST_SetSRID(ST_MakePoint(0, 0), 4326))`,
		f.poiID, f.cityID, "Bar Alta "+f.poiID.String()[:8], category)
	require.NoError(t, err)

	_, err = testDB.Exec(ctx, `
		INSERT INTO user_taste_traits (user_id, trait_key, label, score, confidence, evidence_count)
		VALUES ($1, $2, $3, 0.8, 0.4, $4)`,
		f.userID, category, "Bars", len(events))
	require.NoError(t, err)

	for _, event := range events {
		var feedbackID uuid.UUID
		err = testDB.QueryRow(ctx, `
			INSERT INTO preference_feedback (user_id, poi_id, event, weight)
			VALUES ($1, $2, $3, 1.0) RETURNING id`,
			f.userID, f.poiID.String(), event).Scan(&feedbackID)
		require.NoError(t, err)

		_, err = testDB.Exec(ctx, `
			INSERT INTO taste_trait_evidence (user_id, trait_key, feedback_id, weight, occurred_at)
			VALUES ($1, $2, $3, 1.0, NOW())`, f.userID, category, feedbackID)
		require.NoError(t, err)
	}
	return f
}

// The property the whole phase is for: a belief must be explainable. A trait
// that says "4 signals" with no way to ask which four is an assertion the user
// cannot check.
func TestGetReturnsEvidenceBehindEachTrait(t *testing.T) {
	ctx := context.Background()
	f := seed(t, "bar", "saved", "favorited")

	profile, err := NewService(testDB, nil).Get(ctx, f.userID, true)
	require.NoError(t, err)
	require.Len(t, profile.Traits, 1)

	trait := profile.Traits[0]
	require.Equal(t, "bar", trait.Key)
	require.Len(t, trait.Evidence, 2, "trait returned without the signals behind it")

	// Evidence must name the place, not just an id — the user reads
	// "you saved Bar Alta", not a uuid.
	require.NotEmpty(t, trait.Evidence[0].POIName)
	require.Contains(t, []string{"saved", "favorited"}, trait.Evidence[0].Event)

	require.Equal(t, 2, profile.SignalCount)
	require.True(t, profile.PersonalizationEnabled, "absent settings row must mean enabled")
}

func TestGetWithoutEvidenceSkipsTheJoin(t *testing.T) {
	ctx := context.Background()
	f := seed(t, "cafe", "saved")

	profile, err := NewService(testDB, nil).Get(ctx, f.userID, false)
	require.NoError(t, err)
	require.Len(t, profile.Traits, 1)
	require.Empty(t, profile.Traits[0].Evidence)
}

// Deleting only the trait would be theatre: the next rerank rebuilds it from
// the same signals. Forgetting has to reach the evidence.
func TestForgetTraitRemovesTheUnderlyingSignals(t *testing.T) {
	ctx := context.Background()
	f := seed(t, "museum", "saved", "visited", "skipped")
	svc := NewService(testDB, nil)

	removed, err := svc.ForgetTrait(ctx, f.userID, "museum")
	require.NoError(t, err)
	require.Equal(t, 3, removed)

	profile, err := svc.Get(ctx, f.userID, true)
	require.NoError(t, err)
	require.Empty(t, profile.Traits, "trait survived being forgotten")
	require.Zero(t, profile.SignalCount, "signals survived; the trait would be rebuilt")

	var evidence int
	require.NoError(t, testDB.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM taste_trait_evidence WHERE user_id = $1`, f.userID).Scan(&evidence))
	require.Zero(t, evidence, "evidence rows were orphaned rather than cascaded")
}

// Finer grained: one mistaken save should not cost an otherwise accurate belief.
func TestForgetEvidenceRemovesOnlyThatSignal(t *testing.T) {
	ctx := context.Background()
	f := seed(t, "gallery", "saved", "visited")
	svc := NewService(testDB, nil)

	profile, err := svc.Get(ctx, f.userID, true)
	require.NoError(t, err)
	require.Len(t, profile.Traits[0].Evidence, 2)
	target := profile.Traits[0].Evidence[0].FeedbackID

	require.NoError(t, svc.ForgetEvidence(ctx, f.userID, target))

	after, err := svc.Get(ctx, f.userID, true)
	require.NoError(t, err)
	require.Len(t, after.Traits, 1, "the trait itself should survive")
	require.Len(t, after.Traits[0].Evidence, 1, "only the named signal should be gone")
	require.Equal(t, 1, after.SignalCount)
}

// Another account's feedback id must not be deletable by guessing it.
func TestForgetEvidenceIsScopedToTheOwner(t *testing.T) {
	ctx := context.Background()
	owner := seed(t, "park", "saved")
	stranger := seed(t, "beach", "saved")
	svc := NewService(testDB, nil)

	ownerProfile, err := svc.Get(ctx, owner.userID, true)
	require.NoError(t, err)
	victimFeedback := ownerProfile.Traits[0].Evidence[0].FeedbackID

	err = svc.ForgetEvidence(ctx, stranger.userID, victimFeedback)
	require.ErrorIs(t, err, ErrNotFound)

	// The owner's data is untouched.
	still, err := svc.Get(ctx, owner.userID, true)
	require.NoError(t, err)
	require.Len(t, still.Traits[0].Evidence, 1)
}

// Forgetting must trigger a rebuild of the derived vector and traits, so
// removing one belief cannot leave the rest inconsistent with the record.
func TestForgetTriggersRecompute(t *testing.T) {
	ctx := context.Background()
	f := seed(t, "bakery", "saved")

	var rebuiltFor uuid.UUID
	svc := NewService(testDB, func(_ context.Context, userID uuid.UUID) error {
		rebuiltFor = userID
		return nil
	})

	_, err := svc.ForgetTrait(ctx, f.userID, "bakery")
	require.NoError(t, err)
	require.Equal(t, f.userID, rebuiltFor, "derived state was not rebuilt after a deletion")
}

func TestGetHonoursPersonalizationSetting(t *testing.T) {
	ctx := context.Background()
	f := seed(t, "market", "saved")

	_, err := testDB.Exec(ctx, `
		INSERT INTO personalization_settings (user_id, personalization_enabled)
		VALUES ($1, FALSE)
		ON CONFLICT (user_id) DO UPDATE SET personalization_enabled = FALSE`, f.userID)
	require.NoError(t, err)

	profile, err := NewService(testDB, nil).Get(ctx, f.userID, false)
	require.NoError(t, err)
	require.False(t, profile.PersonalizationEnabled)
	// The record is kept even when learning is switched off — the user can
	// still see and delete it.
	require.Len(t, profile.Traits, 1)
}

func TestForgetTraitRequiresAKey(t *testing.T) {
	_, err := NewService(testDB, nil).ForgetTrait(context.Background(), uuid.New(), "")
	require.Error(t, err)
}
