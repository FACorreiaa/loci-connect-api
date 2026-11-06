//go:build integration

package retrieval

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

func TestVerifiedAccessibility_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.MustPool()
	verified, reported, other := uuid.New(), uuid.New(), uuid.New()

	seed := func(poi uuid.UUID, field, value string, contributors int, expiresIn string) {
		t.Helper()
		_, err := pool.Exec(ctx, `
			INSERT INTO place_facts (poi_id, field, value, confidence, contributor_count, verified_at, expires_at, updated_at)
			VALUES ($1, $2, $3, 0.9, $4, NOW(), NOW() + $5::interval, NOW())`,
			poi.String(), field, value, contributors, expiresIn)
		require.NoError(t, err)
	}
	seed(verified, "accessibility", "step_free", 3, "30 days")
	seed(verified, "accessibility", "accessible_wc", 2, "30 days")
	seed(verified, "accessibility", "lift", 4, "-1 day")       // expired
	seed(verified, "dietary", "vegan", 5, "30 days")           // other field
	seed(reported, "accessibility", "step_free", 1, "30 days") // one contributor: not verified
	seed(other, "accessibility", "none", 2, "30 days")

	got, err := newHistoryAssembler().VerifiedAccessibility(ctx, []uuid.UUID{verified, reported, uuid.Nil})
	require.NoError(t, err)

	tokens := map[string]bool{}
	for _, f := range got[verified] {
		tokens[f.Token] = true
		assert.GreaterOrEqual(t, f.ContributorCount, int32(2))
	}
	assert.Equal(t, map[string]bool{"step_free": true, "accessible_wc": true}, tokens,
		"only unexpired, verified accessibility facts")
	assert.Empty(t, got[reported], "a single report is not verified")
	assert.NotContains(t, got, other, "places not asked about are not returned")
}
