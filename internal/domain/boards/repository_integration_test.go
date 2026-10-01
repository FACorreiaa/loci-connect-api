//go:build integration

package boards

import (
	"context"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRepo(t *testing.T) (Repository, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testsupport.StartPostgres(t)
	testsupport.Truncate(t, pool, "board_sanctions", "board_post_votes", "board_comments", "board_posts", "boards")
	return NewRepository(pool), pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username) VALUES ($1, $2, $3)",
		id, "brd-"+id.String()+"@example.com", "brduser-"+id.String()[:8])
	require.NoError(t, err)
	return id
}

func seedBoard(t *testing.T, repo Repository, owner uuid.UUID, slug string) *Board {
	t.Helper()
	b := &Board{Slug: slug, Name: "Board " + slug, CreatedBy: owner}
	require.NoError(t, repo.CreateBoard(context.Background(), b))
	return b
}

func seedPost(t *testing.T, repo Repository, board *Board, author uuid.UUID, title string) *Post {
	t.Helper()
	p := &Post{BoardID: board.ID, AuthorID: author, Title: title}
	require.NoError(t, repo.CreatePost(context.Background(), p))
	return p
}

func TestBoardSlugUniqueWhileLive_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	u := seedUser(t, pool)

	seedBoard(t, repo, u, "lisbon")
	assert.ErrorIs(t, repo.CreateBoard(ctx, &Board{Slug: "lisbon", Name: "Again", CreatedBy: u}), ErrSlugTaken)

	require.NoError(t, repo.DeleteBoard(ctx, "lisbon", u))
	_, err := repo.GetBoard(ctx, "lisbon")
	assert.ErrorIs(t, err, ErrNotFound)
	// A deleted board frees its slug.
	seedBoard(t, repo, u, "lisbon")
}

func TestVoteCountersStayConsistent_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	author, voter, other := seedUser(t, pool), seedUser(t, pool), seedUser(t, pool)
	p := seedPost(t, repo, seedBoard(t, repo, author, "porto"), author, "Francesinha spots")

	steps := []struct {
		user  uuid.UUID
		value int
		score int
	}{
		{voter, 1, 1},
		{voter, 1, 1}, // repeating a vote changes nothing
		{other, 1, 2},
		{voter, -1, 0}, // flip: -1 up, +1 down
		{voter, 0, 1},  // clear
		{other, 0, 0},
	}
	for i, s := range steps {
		score, err := repo.Vote(ctx, s.user, p.ID, s.value)
		require.NoError(t, err, "step %d", i)
		assert.Equal(t, s.score, score, "step %d", i)
	}

	var up, down, score int
	require.NoError(t, pool.QueryRow(ctx, "SELECT upvotes, downvotes, score FROM board_posts WHERE id = $1", p.ID).Scan(&up, &down, &score))
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{up, down, score})

	_, err := repo.Vote(ctx, voter, p.ID, -1)
	require.NoError(t, err)
	got, err := repo.GetPost(ctx, p.ID, voter, false)
	require.NoError(t, err)
	assert.Equal(t, -1, got.MyVote)
	assert.Equal(t, -1, got.Score)
	got, err = repo.GetPost(ctx, p.ID, uuid.Nil, false)
	require.NoError(t, err)
	assert.Equal(t, 0, got.MyVote, "anonymous viewers have no vote")
}

func TestBanHidesAndLiftRestores_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	admin, troll, reader := seedUser(t, pool), seedUser(t, pool), seedUser(t, pool)
	b := seedBoard(t, repo, admin, "madrid")
	p := seedPost(t, repo, b, troll, "Buy my thing")
	ok := seedPost(t, repo, b, reader, "Tapas crawl")
	c := &Comment{PostID: ok.ID, AuthorID: troll, Body: "spam"}
	require.NoError(t, repo.CreateComment(ctx, c))
	reply := &Comment{PostID: ok.ID, ParentID: &c.ID, AuthorID: reader, Body: "no thanks"}
	require.NoError(t, repo.CreateComment(ctx, reply))

	ban := &Sanction{UserID: troll, Kind: SanctionBan, CreatedBy: admin}
	require.NoError(t, repo.CreateSanction(ctx, ban))

	posts, err := repo.ListPosts(ctx, PostQuery{BoardID: &b.ID, Sort: SortNew, Limit: 10, Viewer: reader})
	require.NoError(t, err)
	require.Len(t, posts, 1)
	assert.Equal(t, ok.ID, posts[0].ID)
	_, err = repo.GetPost(ctx, p.ID, reader, false)
	assert.ErrorIs(t, err, ErrNotFound)

	comments, err := repo.ListComments(ctx, ok.ID, false)
	require.NoError(t, err)
	require.Len(t, comments, 2)
	assert.True(t, comments[0].Deleted, "a banned author's comment is a placeholder")
	assert.Empty(t, comments[0].Body)

	// Admins still see it all.
	posts, err = repo.ListPosts(ctx, PostQuery{BoardID: &b.ID, Sort: SortNew, Limit: 10, IncludeBanned: true})
	require.NoError(t, err)
	assert.Len(t, posts, 2)

	active, err := repo.ActiveSanction(ctx, troll)
	require.NoError(t, err)
	require.NotNil(t, active)
	require.NoError(t, repo.LiftSanction(ctx, ban.ID))
	active, err = repo.ActiveSanction(ctx, troll)
	require.NoError(t, err)
	assert.Nil(t, active)

	posts, err = repo.ListPosts(ctx, PostQuery{BoardID: &b.ID, Sort: SortNew, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, posts, 2, "lifting the ban restores the posts")
}

func TestExpiredSanctionIsInactive_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	admin, u := seedUser(t, pool), seedUser(t, pool)
	past := time.Now().Add(-time.Minute)
	require.NoError(t, repo.CreateSanction(ctx, &Sanction{UserID: u, Kind: SanctionMute, ExpiresAt: &past, CreatedBy: admin}))
	active, err := repo.ActiveSanction(ctx, u)
	require.NoError(t, err)
	assert.Nil(t, active)

	future := time.Now().Add(time.Hour)
	require.NoError(t, repo.CreateSanction(ctx, &Sanction{UserID: u, Kind: SanctionMute, ExpiresAt: &future, CreatedBy: admin}))
	require.NoError(t, repo.CreateSanction(ctx, &Sanction{UserID: u, Kind: SanctionBan, ExpiresAt: &future, CreatedBy: admin}))
	active, err = repo.ActiveSanction(ctx, u)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, SanctionBan, active.Kind, "a ban outranks a mute")
}

func TestNewKeysetAndTopOrder_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	u, v := seedUser(t, pool), seedUser(t, pool)
	b := seedBoard(t, repo, u, "rome")
	var ids []uuid.UUID
	for _, title := range []string{"first", "second", "third", "fourth", "fifth"} {
		ids = append(ids, seedPost(t, repo, b, u, title).ID)
	}

	var seen []uuid.UUID
	var after *PostCursor
	for {
		page, err := repo.ListPosts(ctx, PostQuery{BoardID: &b.ID, Sort: SortNew, After: after, Limit: 2})
		require.NoError(t, err)
		for _, p := range page {
			seen = append(seen, p.ID)
		}
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		after = &PostCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	require.Len(t, seen, 5, "keyset pages cover every post once")
	assert.Equal(t, ids[4], seen[0], "newest first")
	assert.Equal(t, ids[0], seen[4])

	_, err := repo.Vote(ctx, v, ids[1], 1)
	require.NoError(t, err)
	_, err = repo.Vote(ctx, v, ids[3], -1)
	require.NoError(t, err)
	top, err := repo.ListPosts(ctx, PostQuery{BoardID: &b.ID, Sort: SortTop, Limit: 10})
	require.NoError(t, err)
	require.Len(t, top, 5)
	assert.Equal(t, ids[1], top[0].ID)
	assert.Equal(t, ids[3], top[4].ID)

	// Every-board feed includes the board chip.
	all, err := repo.ListPosts(ctx, PostQuery{Sort: SortNew, Limit: 1})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "rome", all[0].BoardSlug)

	// A deleted board's posts leave every feed.
	require.NoError(t, repo.DeleteBoard(ctx, "rome", u))
	all, err = repo.ListPosts(ctx, PostQuery{Sort: SortNew, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestCommentsCountAndParentCheck_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	u := seedUser(t, pool)
	b := seedBoard(t, repo, u, "paris")
	p1 := seedPost(t, repo, b, u, "post one")
	p2 := seedPost(t, repo, b, u, "post two")

	c := &Comment{PostID: p1.ID, AuthorID: u, Body: "hi"}
	require.NoError(t, repo.CreateComment(ctx, c))
	err := repo.CreateComment(ctx, &Comment{PostID: p2.ID, ParentID: &c.ID, AuthorID: u, Body: "wrong thread"})
	assert.ErrorIs(t, err, ErrInvalid)

	got, err := repo.GetPost(ctx, p1.ID, u, false)
	require.NoError(t, err)
	assert.Equal(t, 1, got.CommentCount)

	require.NoError(t, repo.DeleteComment(ctx, c.ID, u))
	assert.ErrorIs(t, repo.DeleteComment(ctx, c.ID, u), ErrNotFound)
	got, err = repo.GetPost(ctx, p1.ID, u, false)
	require.NoError(t, err)
	assert.Equal(t, 0, got.CommentCount)
}

func TestSnapshotOwnership_Integration(t *testing.T) {
	repo, pool := newRepo(t)
	ctx := context.Background()
	owner, other := seedUser(t, pool), seedUser(t, pool)
	cityID, itinID := uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO cities (id, name, country) VALUES ($1, 'Lisbon', 'Portugal')", cityID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_saved_itineraries (id, user_id, primary_city_id, title, description, markdown_content)
		VALUES ($1, $2, $3, 'Lisbon in 3 days', 'Trams and tascas', '')`, itinID, owner, cityID)
	require.NoError(t, err)

	s, err := repo.Snapshot(ctx, KindItinerary, itinID, owner)
	require.NoError(t, err)
	assert.Equal(t, Snapshot{Title: "Lisbon in 3 days", Subtitle: "Trams and tascas", City: "Lisbon"}, s)
	_, err = repo.Snapshot(ctx, KindItinerary, itinID, other)
	assert.ErrorIs(t, err, ErrNotFound)

	s, err = repo.Snapshot(ctx, KindCity, cityID, other)
	require.NoError(t, err)
	assert.Equal(t, "Portugal", s.Subtitle)

	// Round-trips through the post row.
	b := seedBoard(t, repo, owner, "trips")
	p := &Post{BoardID: b.ID, AuthorID: owner, Title: "My trip", Attachment: &Attachment{Kind: KindItinerary, Ref: itinID.String(), Snapshot: s}}
	require.NoError(t, repo.CreatePost(ctx, p))
	got, err := repo.GetPost(ctx, p.ID, owner, false)
	require.NoError(t, err)
	require.NotNil(t, got.Attachment)
	assert.Equal(t, s, got.Attachment.Snapshot)
}
