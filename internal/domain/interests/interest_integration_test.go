//go:build integration

package interests

import (
	"context"
	"fmt"
	"io"
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
	testinterestsDB      *pgxpool.Pool
	testinterestsService Service
	testinterestsRepo    Repository
)

func TestMain(m *testing.M) {
	testinterestsDB = testsupport.MustPool()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	testinterestsRepo = NewRepositoryImpl(testinterestsDB, logger)
	testinterestsService = NewService(testinterestsRepo, logger)
	os.Exit(m.Run())
}

func clearInterestsTable(t *testing.T) {
	t.Helper()
	// Cascades to user_interests via the interest_id FK.
	_, err := testinterestsDB.Exec(context.Background(), "DELETE FROM interests")
	require.NoError(t, err, "Failed to clear interests table")
}

func createTestUserForInterestTests(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := fmt.Sprintf("interest-%s@example.com", uuid.NewString())
	_, err := testinterestsDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, email)
	require.NoError(t, err)
	return id
}

func sp(s string) *string { return &s }
func bp(b bool) *bool      { return &b }

func TestInterestsService_CreateInterest_Integration(t *testing.T) {
	ctx := context.Background()
	clearInterestsTable(t)
	userID := createTestUserForInterestTests(t)
	desc := "A fun outdoor activity"

	t.Run("Create new interest successfully", func(t *testing.T) {
		created, err := testinterestsService.CreateInterest(ctx, "Hiking", &desc, true, userID.String())
		require.NoError(t, err)
		require.NotNil(t, created)
		assert.Equal(t, "Hiking", created.Name)

		// Verify persisted row (column is `active`, not `is_active`).
		var dbName, dbDesc string
		var dbActive bool
		err = testinterestsDB.QueryRow(ctx,
			"SELECT name, description, active FROM user_custom_interests WHERE id = $1", created.ID).
			Scan(&dbName, &dbDesc, &dbActive)
		require.NoError(t, err)
		assert.Equal(t, "Hiking", dbName)
		assert.Equal(t, desc, dbDesc)
		assert.True(t, dbActive)
	})

	t.Run("Create interest with nil description", func(t *testing.T) {
		created, err := testinterestsService.CreateInterest(ctx, "Museums", nil, true, userID.String())
		require.NoError(t, err)
		require.NotNil(t, created)
		assert.Equal(t, "Museums", created.Name)
	})
}

func TestInterestsService_GetAllInterests_Integration(t *testing.T) {
	ctx := context.Background()
	clearInterestsTable(t)
	userID := createTestUserForInterestTests(t)
	desc1 := "Global History"

	interestA, err := testinterestsService.CreateInterest(ctx, "World History", &desc1, true, userID.String())
	require.NoError(t, err)
	interestB, err := testinterestsService.CreateInterest(ctx, "Contemporary Art", nil, true, userID.String())
	require.NoError(t, err)

	interests, err := testinterestsService.GetAllInterests(ctx, userID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(interests), 2)

	foundA, foundB := false, false
	for _, i := range interests {
		if i.ID == interestA.ID {
			foundA = true
			assert.Equal(t, "World History", i.Name)
		}
		if i.ID == interestB.ID {
			foundB = true
		}
	}
	assert.True(t, foundA, "Interest A not found")
	assert.True(t, foundB, "Interest B not found")
}

func TestInterestsService_UpdateInterests_Integration(t *testing.T) {
	ctx := context.Background()
	clearInterestsTable(t)
	userID := createTestUserForInterestTests(t)
	desc := "Original description"

	created, err := testinterestsService.CreateInterest(ctx, "Updatable Interest", &desc, true, userID.String())
	require.NoError(t, err)

	t.Run("Update interest details", func(t *testing.T) {
		params := locitypes.UpdateinterestsParams{
			Name:        sp("Updated Interest Name"),
			Description: sp("This is an updated description."),
			Active:      bp(false),
		}
		err := testinterestsService.UpdateInterests(ctx, userID, created.ID, params)
		require.NoError(t, err)

		var dbName, dbDesc string
		var dbActive bool
		err = testinterestsDB.QueryRow(ctx,
			"SELECT name, description, active FROM user_custom_interests WHERE id = $1", created.ID).
			Scan(&dbName, &dbDesc, &dbActive)
		require.NoError(t, err)
		assert.Equal(t, "Updated Interest Name", dbName)
		assert.Equal(t, "This is an updated description.", dbDesc)
		assert.False(t, dbActive)
	})

	t.Run("Update non-existent interest", func(t *testing.T) {
		params := locitypes.UpdateinterestsParams{Name: sp("NonExistentUpdate")}
		err := testinterestsService.UpdateInterests(ctx, userID, uuid.New(), params)
		require.Error(t, err)
	})
}
