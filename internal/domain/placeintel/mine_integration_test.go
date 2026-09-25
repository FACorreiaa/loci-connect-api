//go:build integration

package placeintel

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	placev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/place"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

// The contribute page could only show totals; a scout had no way to see which
// of their reports were confirmed. ListMyClaims returns them, newest first,
// and only the caller's.
func TestListMyClaims(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	h := NewHandler(pool, nil)
	_, poiID := seedPlace(t, pool)
	alice, bob := seedScout(t, pool), seedScout(t, pool)

	submit(t, h, alice, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, "busy")
	submit(t, h, alice, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL, "quiet")
	submit(t, h, bob, poiID, placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, "busy") // corroborates alice

	res, err := h.ListMyClaims(ctxAs(alice), connect.NewRequest(&placev1.ListMyClaimsRequest{}))
	require.NoError(t, err)
	assert.EqualValues(t, 2, res.Msg.Total)
	require.Len(t, res.Msg.Claims, 2)

	newest, oldest := res.Msg.Claims[0], res.Msg.Claims[1]
	assert.Equal(t, placev1.PlaceFactField_PLACE_FACT_FIELD_NOISE_LEVEL, newest.Field, "newest first")
	assert.Equal(t, "quiet", newest.Value)
	assert.Equal(t, placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_PENDING, newest.Status)
	assert.Equal(t, placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, oldest.Field)
	assert.Equal(t, placev1.PlaceClaimStatus_PLACE_CLAIM_STATUS_ACCEPTED, oldest.Status, "bob's report confirmed it")
	for _, c := range res.Msg.Claims {
		assert.Equal(t, poiID.String(), c.PoiId)
		assert.Equal(t, "Test Cafe", c.PoiName)
		assert.NotEmpty(t, c.ClaimId)
		require.NotNil(t, c.CreatedAt)
	}

	// Paging.
	res, err = h.ListMyClaims(ctxAs(alice), connect.NewRequest(&placev1.ListMyClaimsRequest{Limit: 1, Page: 2}))
	require.NoError(t, err)
	assert.EqualValues(t, 2, res.Msg.Total)
	require.Len(t, res.Msg.Claims, 1)
	assert.Equal(t, placev1.PlaceFactField_PLACE_FACT_FIELD_CROWD_LEVEL, res.Msg.Claims[0].Field)

	// Someone with no claims gets an empty list, not someone else's.
	res, err = h.ListMyClaims(ctxAs(seedScout(t, pool)), connect.NewRequest(&placev1.ListMyClaimsRequest{}))
	require.NoError(t, err)
	assert.Empty(t, res.Msg.Claims)
	assert.Zero(t, res.Msg.Total)

	_, err = h.ListMyClaims(context.Background(), connect.NewRequest(&placev1.ListMyClaimsRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestContributorProfileCarriesBadgeCopy(t *testing.T) {
	pool, _ := testsupport.StartPostgres(t)
	h := NewHandler(pool, nil)
	scout := seedScout(t, pool)

	res, err := h.GetMyContributorProfile(ctxAs(scout), connect.NewRequest(&placev1.GetMyContributorProfileRequest{}))
	require.NoError(t, err)
	assert.Empty(t, res.Msg.BadgeDetails)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO contributor_profiles (user_id, reputation, submitted_claims, accepted_claims, badges)
		VALUES ($1, 40, 12, 10, ARRAY['local-scout'])`, scout)
	require.NoError(t, err)

	res, err = h.GetMyContributorProfile(ctxAs(scout), connect.NewRequest(&placev1.GetMyContributorProfileRequest{}))
	require.NoError(t, err)
	assert.Equal(t, []string{"local-scout"}, res.Msg.Badges, "the slug list stays for older clients")
	require.Len(t, res.Msg.BadgeDetails, 1)
	assert.Equal(t, "local-scout", res.Msg.BadgeDetails[0].Slug)
	assert.Equal(t, "Local scout", res.Msg.BadgeDetails[0].DisplayName)
	assert.NotEmpty(t, res.Msg.BadgeDetails[0].Description)
}
