//go:build integration

package profiles

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The full consent lifecycle against real Postgres: migration 0133's columns,
// the repository's reads and writes, and the CHECK that makes "needs without
// consent" impossible even for a code path that skips the service.
func TestAccessibilityNeeds_Integration(t *testing.T) {
	ctx := context.Background()
	clearUserPreferenceProfilesTable(t)
	userID := createTestUserForProfileTests(t, "a11y")

	t.Run("create without consent is rejected and stores nothing", func(t *testing.T) {
		_, err := testUserProfileService.CreateSearchProfile(ctx, userID, locitypes.CreateUserPreferenceProfileParams{
			ProfileName:        "No consent",
			AccessibilityNeeds: wheelchairNeeds(),
		})
		require.ErrorIs(t, err, ErrAccessibilityConsentRequired)
		profiles, err := testUserProfileService.GetSearchProfiles(ctx, userID)
		require.NoError(t, err)
		for _, p := range profiles {
			assert.NotEqual(t, "No consent", p.ProfileName)
		}
	})

	created, err := testUserProfileService.CreateSearchProfile(ctx, userID, locitypes.CreateUserPreferenceProfileParams{
		ProfileName:               "Lisbon",
		AccessibilityNeeds:        wheelchairNeeds(),
		AccessibilityConsentGiven: true,
	})
	require.NoError(t, err)

	t.Run("create with consent round-trips with a consent time", func(t *testing.T) {
		got, err := testUserProfileService.GetSearchProfile(ctx, userID, created.ID)
		require.NoError(t, err)
		require.NotNil(t, got.AccessibilityNeeds)
		assert.Equal(t, locitypes.MobilityManualWheelchair, got.AccessibilityNeeds.Mobility)
		assert.Equal(t, 10, got.AccessibilityNeeds.MaxWalkMinutesPerLeg)
		require.NotNil(t, got.AccessibilityNeeds.ConsentAt, "consent time must be recorded")
	})

	t.Run("edit without fresh consent keeps the original consent", func(t *testing.T) {
		before, err := testUserProfileService.GetSearchProfile(ctx, userID, created.ID)
		require.NoError(t, err)
		require.NoError(t, testUserProfileService.UpdateSearchProfile(ctx, userID, created.ID, locitypes.UpdateSearchProfileParams{
			AccessibilityNeeds: &locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityPowerWheelchair, NeedsAccessibleToilet: true},
		}))
		after, err := testUserProfileService.GetSearchProfile(ctx, userID, created.ID)
		require.NoError(t, err)
		require.NotNil(t, after.AccessibilityNeeds)
		assert.Equal(t, locitypes.MobilityPowerWheelchair, after.AccessibilityNeeds.Mobility)
		assert.Zero(t, after.AccessibilityNeeds.MaxWalkMinutesPerLeg, "needs are replaced wholesale, not merged")
		assert.True(t, after.AccessibilityNeeds.ConsentAt.Equal(*before.AccessibilityNeeds.ConsentAt))
	})

	t.Run("list endpoint carries the needs", func(t *testing.T) {
		profiles, err := testUserProfileService.GetSearchProfiles(ctx, userID)
		require.NoError(t, err)
		var found bool
		for _, p := range profiles {
			if p.ID == created.ID {
				found = p.AccessibilityNeeds != nil
			}
		}
		assert.True(t, found, "GetSearchProfiles dropped the needs")
	})

	t.Run("clear deletes needs and consent", func(t *testing.T) {
		require.NoError(t, testUserProfileService.UpdateSearchProfile(ctx, userID, created.ID, locitypes.UpdateSearchProfileParams{
			ClearAccessibilityNeeds: true,
		}))
		var needs []byte
		var consentSet bool
		require.NoError(t, testUserProfileDB.QueryRow(ctx,
			`SELECT accessibility_needs, accessibility_consent_at IS NOT NULL FROM user_preference_profiles WHERE id = $1`,
			created.ID).Scan(&needs, &consentSet))
		assert.Nil(t, needs)
		assert.False(t, consentSet)
	})

	t.Run("after clearing, saving again needs fresh consent", func(t *testing.T) {
		err := testUserProfileService.UpdateSearchProfile(ctx, userID, created.ID, locitypes.UpdateSearchProfileParams{
			AccessibilityNeeds: wheelchairNeeds(),
		})
		require.ErrorIs(t, err, ErrAccessibilityConsentRequired)
	})

	t.Run("the CHECK constraint backs the service up", func(t *testing.T) {
		_, err := testUserProfileDB.Exec(ctx,
			`UPDATE user_preference_profiles SET accessibility_needs = '{"blind":true}' WHERE id = $1`, created.ID)
		var pgErr *pgconn.PgError
		require.True(t, errors.As(err, &pgErr), "expected a Postgres error, got %v", err)
		assert.Equal(t, "23514", pgErr.Code)
		assert.Equal(t, "user_preference_profiles_accessibility_consent_chk", pgErr.ConstraintName)
	})

	t.Run("a repository write past the service maps to bad request", func(t *testing.T) {
		repo := NewPostgresUserRepo(testUserProfileDB, slog.New(slog.DiscardHandler))
		err := repo.UpdateSearchProfile(ctx, userID, created.ID, locitypes.UpdateSearchProfileParams{
			AccessibilityNeeds: wheelchairNeeds(),
		})
		require.ErrorIs(t, err, locitypes.ErrBadRequest)
	})
}
