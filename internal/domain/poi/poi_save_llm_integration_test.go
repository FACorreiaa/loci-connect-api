//go:build integration

package poi

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The places a model suggests are written in one batch inside a transaction.
// pgx keeps the connection busy until the batch is closed, so committing with
// the batch still open fails ("conn busy"), the connection is discarded, and
// nothing is saved — while the caller only logs a warning.
func TestSaveLlmPoisToDatabase_Commits(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testDB, slog.New(slog.NewTextHandler(io.Discard, nil)))

	userID := uuid.New()
	_, err := testDB.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`,
		userID, "llm-pois-"+userID.String()+"@loci.test")
	require.NoError(t, err)

	var interactionID uuid.UUID
	require.NoError(t, testDB.QueryRow(ctx,
		`INSERT INTO llm_interactions (user_id, prompt, response) VALUES ($1, 'p', 'r') RETURNING id`,
		userID).Scan(&interactionID))

	suffix := uuid.NewString()
	pois := []locitypes.POIDetailedInfo{
		{ID: uuid.New(), Name: "Prado " + suffix, Latitude: 40.4138, Longitude: -3.6921, Category: "Museum"},
		{ID: uuid.New(), Name: "Retiro " + suffix, Latitude: 40.4153, Longitude: -3.6845, Category: "Park"},
	}

	require.NoError(t, repo.SaveLlmPoisToDatabase(ctx, userID, pois, nil, interactionID))

	var n int
	require.NoError(t, testDB.QueryRow(ctx,
		`SELECT count(*) FROM llm_suggested_pois WHERE llm_interaction_id = $1`, interactionID).Scan(&n))
	require.Equal(t, len(pois), n, "the batch must be committed")
}
