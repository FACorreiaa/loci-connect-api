//go:build integration

package social

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The follow graph on the real schema: public follows, a private account's
// requests, friends as mutual follows (also through the friendships view that
// older readers use), the legacy friend RPCs, and blocks.
func TestFollowsEndToEnd(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testSocialDB)
	svc := NewService(repo, nil, nil)
	ana, rui, eva := newSocialUser(t), newSocialUser(t), newSocialUser(t)

	// Ana follows Rui: one way, not friends.
	state, followsYou, _, err := svc.Follow(ctx, ana, rui)
	require.NoError(t, err)
	assert.Equal(t, FollowFollowing, state)
	assert.False(t, followsYou)
	friends, _, err := repo.Relation(ctx, ana, rui)
	require.NoError(t, err)
	assert.False(t, friends, "a one-way follow made friends")

	// Older clients see it as Ana's request and Rui's incoming request.
	incoming, err := svc.ListFriendRequests(ctx, rui, true)
	require.NoError(t, err)
	require.Len(t, incoming, 1)
	assert.Equal(t, ana.String(), incoming[0].From.GetId())

	// Rui accepts the "request": follows back, and now they are friends.
	_, err = svc.Respond(ctx, rui, incoming[0].ID, true)
	require.NoError(t, err)
	friends, _, err = repo.Relation(ctx, ana, rui)
	require.NoError(t, err)
	assert.True(t, friends)
	var viaView int
	require.NoError(t, testSocialDB.QueryRow(ctx,
		`SELECT COUNT(*) FROM friendships WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`,
		ana, rui).Scan(&viaView))
	assert.Equal(t, 2, viaView, "the friendships view does not show the mutual follow both ways")
	stats, err := repo.Stats(ctx, ana, nil)
	require.NoError(t, err)
	assert.Equal(t, Stats{Friends: 1, Followers: 1, Following: 1}, stats)

	// Eva is private: following her waits for approval.
	_, err = svc.SetPrivate(ctx, eva, true)
	require.NoError(t, err)
	state, _, reqID, err := svc.Follow(ctx, ana, eva)
	require.NoError(t, err)
	assert.Equal(t, FollowRequested, state)
	visible, _, err := svc.CanView(ctx, ana, eva)
	require.NoError(t, err)
	assert.False(t, visible, "a pending request saw a private account")
	_, _, _, err = svc.ListFollows(ctx, ana, eva, true, 0, "")
	assert.ErrorIs(t, err, ErrPrivate)
	edges, err := repo.Edges(ctx, eva, []uuid.UUID{ana})
	require.NoError(t, err)
	assert.True(t, edges[ana].Incoming)

	require.NoError(t, svc.RespondFollow(ctx, eva, reqID, true))
	visible, _, err = svc.CanView(ctx, ana, eva)
	require.NoError(t, err)
	assert.True(t, visible, "an approved follower cannot see a private account")
	followers, _, total, err := svc.ListFollows(ctx, ana, eva, true, 0, "")
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, followers, 1)
	assert.Equal(t, FollowSelf, followers[0].Edge.State)

	// Going public approves whoever is still waiting.
	_, _, _, err = svc.Follow(ctx, rui, eva)
	require.NoError(t, err)
	set, err := svc.SetPrivate(ctx, eva, false)
	require.NoError(t, err)
	assert.Zero(t, set.Pending)
	edges, err = repo.Edges(ctx, rui, []uuid.UUID{eva})
	require.NoError(t, err)
	assert.Equal(t, FollowFollowing, edges[eva].State)

	// A block ends follows both ways.
	require.NoError(t, svc.Block(ctx, ana, rui))
	friends, blocked, err := repo.Relation(ctx, ana, rui)
	require.NoError(t, err)
	assert.False(t, friends)
	assert.True(t, blocked)
	following, err := repo.FollowingIDs(ctx, rui)
	require.NoError(t, err)
	assert.NotContains(t, following, ana)
}

// Migration 0124 on rows that existed before it: every friendship becomes a
// follow each way, a pending request becomes a follow, a pending request
// between a blocked pair does not, and a declined one does not. The harness
// migrates an empty database, so the Down section is run to get the old
// tables back, rows are seeded, and Up is run again.
func TestMigration0124CarriesFriendships(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../../pkg/db/migrations/0124_follows.up.sql")
	require.NoError(t, err)
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	require.True(t, ok)
	_, err = testSocialDB.Exec(ctx, down)
	require.NoError(t, err)

	ana, rui, eva, ivo, ze := newSocialUser(t), newSocialUser(t), newSocialUser(t), newSocialUser(t), newSocialUser(t)
	_, err = testSocialDB.Exec(ctx, `
		INSERT INTO friendships (user_id, friend_id) VALUES ($1, $2), ($2, $1)`, ana, rui)
	require.NoError(t, err)
	_, err = testSocialDB.Exec(ctx, `
		INSERT INTO friend_requests (from_user, to_user, status) VALUES
		  ($1, $2, 'pending'), ($3, $1, 'declined'), ($4, $1, 'pending')`, eva, ana, ivo, ze)
	require.NoError(t, err)
	_, err = testSocialDB.Exec(ctx, `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, ana, ze)
	require.NoError(t, err)

	_, err = testSocialDB.Exec(ctx, up)
	require.NoError(t, err)

	repo := NewRepository(testSocialDB)
	friends, _, err := repo.Relation(ctx, ana, rui)
	require.NoError(t, err)
	assert.True(t, friends, "a friendship did not become a mutual follow")
	edges, err := repo.Edges(ctx, ana, []uuid.UUID{eva, ivo, ze})
	require.NoError(t, err)
	assert.True(t, edges[eva].FollowsYou, "a pending request did not become a follow")
	assert.False(t, edges[ivo].FollowsYou, "a declined request became a follow")
	assert.False(t, edges[ze].FollowsYou, "a request across a block became a follow")
	var legacy int
	require.NoError(t, testSocialDB.QueryRow(ctx,
		`SELECT COUNT(*) FROM friendships_legacy WHERE user_id = $1`, ana).Scan(&legacy))
	assert.Equal(t, 1, legacy, "the old table was not kept")
}
