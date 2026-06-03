//go:build integration

package profiles

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	interestsdomain "github.com/FACorreiaa/loci-connect-api/internal/domain/interests"
	tagsdomain "github.com/FACorreiaa/loci-connect-api/internal/domain/tags"
	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testUserProfileDB      *pgxpool.Pool
	testUserProfileService Service
)

func bp(b bool) *bool         { return &b }
func f64p(v float64) *float64 { return &v }

func TestMain(m *testing.M) {
	testUserProfileDB = testsupport.MustPool()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	prefRepo := NewPostgresUserRepo(testUserProfileDB, logger)
	intRepo := interestsdomain.NewRepositoryImpl(testUserProfileDB, logger)
	tagRepo := tagsdomain.NewRepositoryImpl(testUserProfileDB, logger)
	testUserProfileService = NewUserProfilesService(prefRepo, intRepo, tagRepo, logger)
	os.Exit(m.Run())
}

func clearUserPreferenceProfilesTable(t *testing.T) {
	t.Helper()
	// Cascades to user_profile_interests via FK.
	_, err := testUserProfileDB.Exec(context.Background(), "DELETE FROM user_preference_profiles")
	require.NoError(t, err)
}

func createTestUserForProfileTests(t *testing.T, suffix string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := fmt.Sprintf("profile-%s-%s@example.com", suffix, uuid.NewString())
	_, err := testUserProfileDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, email)
	require.NoError(t, err)
	return id
}

func TestProfilesServiceImpl_Integration(t *testing.T) {
	ctx := context.Background()
	clearUserPreferenceProfilesTable(t)

	userID1 := createTestUserForProfileTests(t, "one")
	userID2 := createTestUserForProfileTests(t, "two")

	var profileID1 uuid.UUID
	var profileID2 uuid.UUID

	t.Run("CreateSearchProfile", func(t *testing.T) {
		profile1, err := testUserProfileService.CreateSearchProfile(ctx, userID1,
			locitypes.CreateUserPreferenceProfileParams{
				ProfileName:    "Weekend Getaway",
				IsDefault:      bp(true),
				SearchRadiusKm: f64p(10.0),
			})
		require.NoError(t, err)
		require.NotNil(t, profile1)
		profileID1 = profile1.ID
		assert.Equal(t, "Weekend Getaway", profile1.ProfileName)
		assert.True(t, profile1.IsDefault)

		profile2, err := testUserProfileService.CreateSearchProfile(ctx, userID1,
			locitypes.CreateUserPreferenceProfileParams{
				ProfileName: "Quick Bites",
				IsDefault:   bp(false),
			})
		require.NoError(t, err)
		require.NotNil(t, profile2)
		profileID2 = profile2.ID
		assert.Equal(t, "Quick Bites", profile2.ProfileName)
		assert.False(t, profile2.IsDefault)
	})

	t.Run("GetSearchProfiles for user1", func(t *testing.T) {
		// Each user has an auto-created "Default" profile, so assert membership
		// of the profiles we created rather than an exact count.
		profiles, err := testUserProfileService.GetSearchProfiles(ctx, userID1)
		require.NoError(t, err)
		assert.True(t, containsProfile(profiles, profileID1), "should contain profile 1")
		assert.True(t, containsProfile(profiles, profileID2), "should contain profile 2")
	})

	t.Run("GetSearchProfile for existing profile", func(t *testing.T) {
		profile, err := testUserProfileService.GetSearchProfile(ctx, userID1, profileID1)
		require.NoError(t, err)
		require.NotNil(t, profile)
		assert.Equal(t, "Weekend Getaway", profile.ProfileName)
	})

	t.Run("GetDefaultSearchProfile for user1", func(t *testing.T) {
		defaultProfile, err := testUserProfileService.GetDefaultSearchProfile(ctx, userID1)
		require.NoError(t, err)
		require.NotNil(t, defaultProfile)
		assert.Equal(t, "Weekend Getaway", defaultProfile.ProfileName)
		assert.True(t, defaultProfile.IsDefault)
	})

	t.Run("UpdateSearchProfile", func(t *testing.T) {
		err := testUserProfileService.UpdateSearchProfile(ctx, userID1, profileID1,
			locitypes.UpdateSearchProfileParams{
				ProfileName:    "Updated Weekend Adventure",
				SearchRadiusKm: f64p(15.5),
			})
		require.NoError(t, err)

		profile, err := testUserProfileService.GetSearchProfile(ctx, userID1, profileID1)
		require.NoError(t, err)
		assert.Equal(t, "Updated Weekend Adventure", profile.ProfileName)
		assert.InDelta(t, 15.5, profile.SearchRadiusKm, 0.001)
	})

	t.Run("SetDefaultSearchProfile", func(t *testing.T) {
		err := testUserProfileService.SetDefaultSearchProfile(ctx, userID1, profileID2)
		require.NoError(t, err)

		defaultProfile, err := testUserProfileService.GetDefaultSearchProfile(ctx, userID1)
		require.NoError(t, err)
		require.NotNil(t, defaultProfile)
		assert.Equal(t, profileID2, defaultProfile.ID)
		assert.True(t, defaultProfile.IsDefault)

		oldDefault, err := testUserProfileService.GetSearchProfile(ctx, userID1, profileID1)
		require.NoError(t, err)
		assert.False(t, oldDefault.IsDefault)
	})

	t.Run("DeleteSearchProfile", func(t *testing.T) {
		err := testUserProfileService.DeleteSearchProfile(ctx, userID1, profileID1)
		require.NoError(t, err)

		_, err = testUserProfileService.GetSearchProfile(ctx, userID1, profileID1)
		require.Error(t, err)

		profiles, _ := testUserProfileService.GetSearchProfiles(ctx, userID1)
		assert.False(t, containsProfile(profiles, profileID1), "deleted profile should be gone")
		assert.True(t, containsProfile(profiles, profileID2), "profile 2 should remain")
	})

	t.Run("GetSearchProfiles for user2 does not see user1's profiles", func(t *testing.T) {
		profiles, err := testUserProfileService.GetSearchProfiles(ctx, userID2)
		require.NoError(t, err)
		assert.False(t, containsProfile(profiles, profileID1))
		assert.False(t, containsProfile(profiles, profileID2))
	})
}

func containsProfile(profiles []locitypes.UserPreferenceProfileResponse, id uuid.UUID) bool {
	for _, p := range profiles {
		if p.ID == id {
			return true
		}
	}
	return false
}
