//go:build integration

package profiles

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func prefsRepo() *RepositoryImpl {
	return NewPostgresUserRepo(testUserProfileDB, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// seedProfile creates a user and returns its default preference profile.
//
// The profile is not inserted here: a trigger from migration 0008 creates a
// "Default" profile for every new user, and a partial unique index enforces one
// default per user — so inserting another would collide. A second trigger from
// 0018 seeds rows in all four domain preference tables.
func seedProfile(t *testing.T, name string) (userID, profileID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	userID = createTestUserForProfileTests(t, name)
	err := testUserProfileDB.QueryRow(ctx, `
		SELECT id FROM user_preference_profiles
		WHERE user_id = $1 AND is_default = TRUE`, userID).Scan(&profileID)
	require.NoError(t, err, "expected the trigger to create a default profile")
	return userID, profileID
}

func writeDiningPrefs(t *testing.T, profileID uuid.UUID, prefs locitypes.DiningPreferences) {
	t.Helper()
	raw, err := json.Marshal(prefs)
	require.NoError(t, err)
	_, err = testUserProfileDB.Exec(context.Background(), `
		INSERT INTO user_dining_preferences (user_preference_profile_id, dining_filters)
		VALUES ($1, $2)
		ON CONFLICT (user_preference_profile_id) DO UPDATE SET dining_filters = $2`,
		profileID, raw)
	require.NoError(t, err)
}

// The regression this guards: these four tables were written by the preferences
// UI and read by nothing, so a user's stated dietary needs and budget never
// reached a prompt. GetSearchProfile must now carry them back.
func TestGetSearchProfile_ReturnsDomainPreferences(t *testing.T) {
	ctx := context.Background()
	userID, profileID := seedProfile(t, "readback")

	writeDiningPrefs(t, profileID, locitypes.DiningPreferences{
		CuisineTypes: []string{"portuguese", "japanese"},
		DietaryNeeds: []string{"vegetarian"},
		ChainVsLocal: "local_only",
	})

	got, err := prefsRepo().GetSearchProfile(ctx, userID, profileID)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.NotNil(t, got.DiningPreferences,
		"dining preferences were written but not read back")
	require.Equal(t, []string{"portuguese", "japanese"}, got.DiningPreferences.CuisineTypes)
	require.Equal(t, []string{"vegetarian"}, got.DiningPreferences.DietaryNeeds)
	require.Equal(t, "local_only", got.DiningPreferences.ChainVsLocal)
}

func TestGetDefaultSearchProfile_ReturnsDomainPreferences(t *testing.T) {
	ctx := context.Background()
	userID, profileID := seedProfile(t, "default-readback")

	writeDiningPrefs(t, profileID, locitypes.DiningPreferences{
		CuisineTypes: []string{"peruvian"},
	})

	got, err := prefsRepo().GetDefaultSearchProfile(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, profileID, got.ID)

	require.NotNil(t, got.DiningPreferences,
		"the default-profile path did not load domain preferences")
	require.Equal(t, []string{"peruvian"}, got.DiningPreferences.CuisineTypes)
}

// A profile with no rows in the domain tables must load cleanly with nil
// pointers — that is exactly what the prompt renderer branches on.
func TestGetSearchProfile_MissingDomainPreferencesAreNil(t *testing.T) {
	ctx := context.Background()
	userID, profileID := seedProfile(t, "empty")

	// Remove whatever the 0018 trigger seeded.
	for _, table := range []string{
		"user_accommodation_preferences",
		"user_dining_preferences",
		"user_activity_preferences",
		"user_itinerary_preferences",
	} {
		_, err := testUserProfileDB.Exec(ctx,
			"DELETE FROM "+table+" WHERE user_preference_profile_id = $1", profileID)
		require.NoError(t, err)
	}

	got, err := prefsRepo().GetSearchProfile(ctx, userID, profileID)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.Nil(t, got.AccommodationPreferences)
	require.Nil(t, got.DiningPreferences)
	require.Nil(t, got.ActivityPreferences)
	require.Nil(t, got.ItineraryPreferences)
}

// A blob that no longer matches the struct must cost only that one domain, not
// the whole profile. Stored JSON outlives the code that wrote it.
func TestGetSearchProfile_MalformedBlobDoesNotFailTheProfile(t *testing.T) {
	ctx := context.Background()
	userID, profileID := seedProfile(t, "malformed")

	_, err := testUserProfileDB.Exec(ctx, `
		INSERT INTO user_dining_preferences (user_preference_profile_id, dining_filters)
		VALUES ($1, $2)
		ON CONFLICT (user_preference_profile_id) DO UPDATE SET dining_filters = $2`,
		profileID, []byte(`{"cuisine_types": "not-an-array"}`))
	require.NoError(t, err)

	got, err := prefsRepo().GetSearchProfile(ctx, userID, profileID)
	require.NoError(t, err, "one bad blob must not fail the whole profile read")
	require.NotNil(t, got)
	require.Equal(t, profileID, got.ID)
	require.Nil(t, got.DiningPreferences, "unreadable blob should be skipped, not partially applied")
}
