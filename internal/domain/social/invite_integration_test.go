//go:build integration

package social

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
)

var testSocialDB *pgxpool.Pool

func TestMain(m *testing.M) {
	testSocialDB = testsupport.MustPool()
	os.Exit(m.Run())
}

func newSocialUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testSocialDB.Exec(context.Background(),
		"INSERT INTO users (id, email) VALUES ($1, $2)", id, "invite-"+id.String()+"@loci.test")
	require.NoError(t, err)
	return id
}

func invitedBy(t *testing.T, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var by *uuid.UUID
	require.NoError(t, testSocialDB.QueryRow(context.Background(),
		"SELECT invited_by_user_id FROM users WHERE id = $1", id).Scan(&by))
	return by
}

// Migration 0115 on rows that existed before it: an expiring code becomes
// permanent and keeps its value, and an account with no code gets one of the
// same shape newCode() makes. The harness migrates an empty database, so the
// Up section is run again here over seeded rows; it is written to be
// re-runnable.
func TestMigration0115Backfill(t *testing.T) {
	ctx := context.Background()
	withCode, withoutCode := newSocialUser(t), newSocialUser(t)
	_, err := testSocialDB.Exec(ctx,
		"INSERT INTO user_invites (user_id, code, expires_at) VALUES ($1, $2, $3)",
		withCode, "legacy-"+withCode.String()[:8], time.Now().Add(time.Hour))
	require.NoError(t, err)

	raw, err := os.ReadFile("../../../pkg/db/migrations/0115_invite_attribution.up.sql")
	require.NoError(t, err)
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	require.True(t, ok)
	_, err = testSocialDB.Exec(ctx, up)
	require.NoError(t, err)

	repo := NewRepository(testSocialDB)
	kept, err := repo.InviteFor(ctx, withCode)
	require.NoError(t, err)
	assert.Equal(t, "legacy-"+withCode.String()[:8], kept.Code, "an existing code keeps its value")
	assert.Nil(t, kept.ExpiresAt, "an existing code no longer expires")

	minted, err := repo.InviteFor(ctx, withoutCode)
	require.NoError(t, err, "an account without a code got one")
	assert.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]{12}$`), minted.Code)
	assert.Nil(t, minted.ExpiresAt)
}

// The inviter is written once, never to yourself, and only from a live code.
func TestOnSignupRecordsInviterOnce(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewRepository(testSocialDB), nil, nil)
	ana, rui, eva := newSocialUser(t), newSocialUser(t), newSocialUser(t)
	anaInv, err := svc.MyInvite(ctx, ana)
	require.NoError(t, err)
	assert.Nil(t, anaInv.ExpiresAt, "a new code does not expire")

	svc.OnSignup(ctx, ana, anaInv.Code)
	assert.Nil(t, invitedBy(t, ana), "nobody invites themselves")

	svc.OnSignup(ctx, rui, "no-such-code")
	assert.Nil(t, invitedBy(t, rui), "an unknown code records nothing")
	_, err = NewRepository(testSocialDB).InviteFor(ctx, rui)
	require.NoError(t, err, "signup gives the account its own code")

	svc.OnSignup(ctx, rui, anaInv.Code)
	require.NotNil(t, invitedBy(t, rui))
	assert.Equal(t, ana, *invitedBy(t, rui))

	evaInv, err := svc.MyInvite(ctx, eva)
	require.NoError(t, err)
	svc.OnSignup(ctx, rui, evaInv.Code)
	assert.Equal(t, ana, *invitedBy(t, rui), "the inviter is never overwritten")

	// Deleting the inviter leaves the account, without an inviter.
	_, err = testSocialDB.Exec(ctx, "DELETE FROM users WHERE id = $1", ana)
	require.NoError(t, err)
	assert.Nil(t, invitedBy(t, rui))
}
