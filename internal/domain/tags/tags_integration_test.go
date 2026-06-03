//go:build integration

package tags

import (
	"context"
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
	testtagsDB      *pgxpool.Pool
	testtagsService tagsService
)

func TestMain(m *testing.M) {
	testtagsDB = testsupport.MustPool()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	realRepo := NewRepositoryImpl(testtagsDB, logger)
	testtagsService = NewtagsService(realRepo, logger)
	os.Exit(m.Run())
}

func clearUserPersonalTagsTable(t *testing.T) {
	t.Helper()
	_, err := testtagsDB.Exec(context.Background(), "DELETE FROM user_personal_tags")
	require.NoError(t, err, "Failed to clear user_personal_tags table")
}

func createTestUserForTagTests(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := "tag-" + id.String() + "@example.com"
	_, err := testtagsDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, email)
	require.NoError(t, err, "Failed to insert test user for tags")
	return id
}

func containsTag(tags []*locitypes.Tags, id uuid.UUID) bool {
	for _, tag := range tags {
		if tag.ID == id {
			return true
		}
	}
	return false
}

// Note: GetTags returns the merged set of global tags plus the user's personal
// tags, so the assertions below check membership of the created personal tags
// rather than an exact count.
func TestTagsServiceImpl_Integration(t *testing.T) {
	ctx := context.Background()
	clearUserPersonalTagsTable(t)

	userID := createTestUserForTagTests(t)

	var veganTagID, quietTagID uuid.UUID

	t.Run("CreateTag", func(t *testing.T) {
		tag, err := testtagsService.CreateTag(ctx, userID, locitypes.CreatePersonalTagParams{
			Name:        "Vegan Options",
			Description: "Places with good vegan food",
		})
		require.NoError(t, err)
		require.NotNil(t, tag)
		veganTagID = tag.ID
		assert.Equal(t, "Vegan Options", tag.Name)
		require.NotNil(t, tag.Description)
		assert.Equal(t, "Places with good vegan food", *tag.Description)
		assert.Equal(t, userID, tag.UserID)

		tag2, err := testtagsService.CreateTag(ctx, userID, locitypes.CreatePersonalTagParams{Name: "Quiet Study"})
		require.NoError(t, err)
		require.NotNil(t, tag2)
		quietTagID = tag2.ID
	})

	t.Run("GetTags includes the user's personal tags", func(t *testing.T) {
		tags, err := testtagsService.GetTags(ctx, userID)
		require.NoError(t, err)
		assert.True(t, containsTag(tags, veganTagID), "should contain the Vegan tag")
		assert.True(t, containsTag(tags, quietTagID), "should contain the Quiet Study tag")
	})

	t.Run("GetTag returns an existing personal tag", func(t *testing.T) {
		tag, err := testtagsService.GetTag(ctx, userID, veganTagID)
		require.NoError(t, err)
		require.NotNil(t, tag)
		assert.Equal(t, "Vegan Options", tag.Name)
	})

	t.Run("Update tag", func(t *testing.T) {
		err := testtagsService.Update(ctx, userID, veganTagID, locitypes.UpdatePersonalTagParams{
			Name:        "Excellent Vegan Food",
			Description: "Top-tier vegan dishes",
		})
		require.NoError(t, err)
	})

	t.Run("Delete tag", func(t *testing.T) {
		err := testtagsService.DeleteTag(ctx, userID, quietTagID)
		require.NoError(t, err)

		_, err = testtagsService.GetTag(ctx, userID, quietTagID)
		require.Error(t, err, "deleted tag should no longer be retrievable")
	})
}
