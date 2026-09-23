//go:build integration

package user

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testUserDB      *pgxpool.Pool
	testUserService UserService
	testUserRepo    UserRepo
)

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

// TestMain normally starts (or reuses) the shared, fully-migrated Postgres
// testcontainer via testsupport. Set PUSH_TEST_DSN to point at a real,
// already-migrated Postgres instead — used to exercise the push_devices /
// search_finished migration against a local DB without invoking goose or
// docker. Run with: PUSH_TEST_DSN=postgres://... go test -tags=integration
// ./internal/domain/user/
func TestMain(m *testing.M) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if dsn := os.Getenv("PUSH_TEST_DSN"); dsn != "" {
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			log.Fatalf("user integration test: connect to PUSH_TEST_DSN: %v", err)
		}
		testUserDB = pool
	} else {
		testUserDB = testsupport.MustPool()
	}
	testUserRepo = NewPostgresUserRepo(testUserDB, logger)
	testUserService = NewUserService(testUserRepo, logger)
	os.Exit(m.Run())
}

func clearUsersTable(t *testing.T) {
	t.Helper()
	_, err := testUserDB.Exec(context.Background(), "DELETE FROM users")
	require.NoError(t, err, "Failed to clear users table")
}

// createTestUserDirectly inserts a user row and returns its id. The users table
// columns are firstname/lastname (no underscore).
func createTestUserDirectly(t *testing.T, username, email, firstname, lastname string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testUserDB.QueryRow(context.Background(),
		"INSERT INTO users (username, email, password_hash, firstname, lastname) VALUES ($1, $2, $3, $4, $5) RETURNING id",
		username, email, "hashedpassword", firstname, lastname).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestServiceUserImpl_UserProfile_Integration(t *testing.T) {
	ctx := context.Background()
	clearUsersTable(t)

	createdUserID := createTestUserDirectly(t, "integ_test_user", "integ@example.com", "Integ", "Test")

	t.Run("Get existing user profile", func(t *testing.T) {
		profile, err := testUserService.GetUserProfile(ctx, createdUserID)
		require.NoError(t, err)
		require.NotNil(t, profile)
		assert.Equal(t, createdUserID, profile.ID)
		require.NotNil(t, profile.Username)
		assert.Equal(t, "integ_test_user", *profile.Username)
		assert.Equal(t, "integ@example.com", profile.Email)
		require.NotNil(t, profile.Firstname)
		assert.Equal(t, "Integ", *profile.Firstname)
		require.NotNil(t, profile.Lastname)
		assert.Equal(t, "Test", *profile.Lastname)
	})

	t.Run("Get non-existent user profile", func(t *testing.T) {
		nonExistentID := uuid.New()
		_, err := testUserService.GetUserProfile(ctx, nonExistentID)
		require.Error(t, err)
	})

	t.Run("Update user profile", func(t *testing.T) {
		updateParams := locitypes.UpdateProfileParams{
			Username:    sp("integ_test_user_updated"),
			Firstname:   sp("IntegrationUpdated"),
			Lastname:    sp("TestUpdated"),
			PhoneNumber: sp("0987654321"),
			Country:     sp("Testlandia"),
			City:        sp("IntegCity"),
		}
		err := testUserService.UpdateUserProfile(ctx, createdUserID, updateParams)
		require.NoError(t, err)

		updatedProfile, err := testUserService.GetUserProfile(ctx, createdUserID)
		require.NoError(t, err)
		require.NotNil(t, updatedProfile)
		require.NotNil(t, updatedProfile.Username)
		assert.Equal(t, "integ_test_user_updated", *updatedProfile.Username)
		require.NotNil(t, updatedProfile.Firstname)
		assert.Equal(t, "IntegrationUpdated", *updatedProfile.Firstname)
		require.NotNil(t, updatedProfile.Lastname)
		assert.Equal(t, "TestUpdated", *updatedProfile.Lastname)
		require.NotNil(t, updatedProfile.PhoneNumber)
		assert.Equal(t, "0987654321", *updatedProfile.PhoneNumber)
		require.NotNil(t, updatedProfile.Country)
		assert.Equal(t, "Testlandia", *updatedProfile.Country)
		require.NotNil(t, updatedProfile.City)
		assert.Equal(t, "IntegCity", *updatedProfile.City)
	})
}

func TestServiceUserImpl_UserStatus_Integration(t *testing.T) {
	ctx := context.Background()
	clearUsersTable(t)

	userID := createTestUserDirectly(t, "status_user", "status@example.com", "", "")

	t.Run("Update Last Login", func(t *testing.T) {
		err := testUserService.UpdateLastLogin(ctx, userID)
		require.NoError(t, err)
	})

	t.Run("Mark Email As Verified", func(t *testing.T) {
		err := testUserService.MarkEmailAsVerified(ctx, userID)
		require.NoError(t, err)

		profile, err := testUserService.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		assert.NotNil(t, profile.EmailVerifiedAt, "email_verified_at should be set after verification")
	})

	t.Run("Deactivate and Reactivate User", func(t *testing.T) {
		err := testUserService.DeactivateUser(ctx, userID)
		require.NoError(t, err)
		// GetUserProfile only returns active users, so verify is_active directly.
		var active bool
		err = testUserDB.QueryRow(ctx, "SELECT is_active FROM users WHERE id = $1", userID).Scan(&active)
		require.NoError(t, err)
		assert.False(t, active)

		err = testUserService.ReactivateUser(ctx, userID)
		require.NoError(t, err)
		profile, err := testUserService.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		assert.True(t, profile.IsActive)
	})
}

// TestServiceUserImpl_NotificationSettings_Integration does not call
// clearUsersTable: unlike the other integration tests in this file, it may
// run against a real, already-populated local DB (via PUSH_TEST_DSN) where
// other tables reference existing users, so it creates its own uniquely
// named users instead of touching the whole table.
func TestServiceUserImpl_NotificationSettings_Integration(t *testing.T) {
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	t.Run("fresh user's search_finished defaults to on", func(t *testing.T) {
		userID := createTestUserDirectly(t, "notif_fresh_"+suffix, "notif_fresh_"+suffix+"@example.com", "Notif", "Fresh")

		settings, err := testUserService.GetNotificationSettings(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, settings)
		assert.True(t, settings.SearchFinished, "search_finished must default to true for a never-configured account")
	})

	t.Run("updating search_finished alone leaves the other switches untouched", func(t *testing.T) {
		userID := createTestUserDirectly(t, "notif_update_"+suffix, "notif_update_"+suffix+"@example.com", "Notif", "Update")

		// Seed recommendations/trip_reminders away from their defaults so a
		// later search_finished-only update can prove it did not touch them.
		seeded, err := testUserService.UpdateNotificationSettings(ctx, userID, locitypes.UpdateNotificationSettingsParams{
			Recommendations: bp(true),
			TripReminders:   bp(true),
		})
		require.NoError(t, err)
		require.True(t, seeded.Recommendations)
		require.True(t, seeded.TripReminders)
		require.True(t, seeded.SearchFinished)

		updated, err := testUserService.UpdateNotificationSettings(ctx, userID, locitypes.UpdateNotificationSettingsParams{
			SearchFinished: bp(false),
		})
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.False(t, updated.SearchFinished)
		assert.True(t, updated.Recommendations, "recommendations must be unchanged by a search_finished-only update")
		assert.True(t, updated.TripReminders, "trip_reminders must be unchanged by a search_finished-only update")
	})
}
