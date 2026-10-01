package boards

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRepo keeps just enough state for the service's permission, limit and
// pruning rules; SQL behaviour is covered by the integration test.
type fakeRepo struct {
	Repository
	boards    map[string]*Board
	posts     map[uuid.UUID]*Post
	comments  map[uuid.UUID]*Comment
	listed    []*Comment
	sanctions map[uuid.UUID]*Sanction
	counts    map[string]int
	snapshots map[uuid.UUID]uuid.UUID // ref -> owner
	votes     map[uuid.UUID]int
	deleted   []uuid.UUID
	lastQuery PostQuery
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		boards:    map[string]*Board{},
		posts:     map[uuid.UUID]*Post{},
		comments:  map[uuid.UUID]*Comment{},
		sanctions: map[uuid.UUID]*Sanction{},
		counts:    map[string]int{},
		snapshots: map[uuid.UUID]uuid.UUID{},
		votes:     map[uuid.UUID]int{},
	}
}

func (f *fakeRepo) CreateBoard(_ context.Context, b *Board) error {
	if _, ok := f.boards[b.Slug]; ok {
		return ErrSlugTaken
	}
	b.ID = uuid.New()
	f.boards[b.Slug] = b
	return nil
}

func (f *fakeRepo) GetBoard(_ context.Context, slug string) (*Board, error) {
	if b, ok := f.boards[slug]; ok {
		return b, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) DeleteBoard(_ context.Context, slug string, _ uuid.UUID) error {
	if _, ok := f.boards[slug]; !ok {
		return ErrNotFound
	}
	delete(f.boards, slug)
	return nil
}

func (f *fakeRepo) CreatePost(_ context.Context, p *Post) error {
	p.ID = uuid.New()
	f.posts[p.ID] = p
	return nil
}

func (f *fakeRepo) GetPost(_ context.Context, id, _ uuid.UUID, includeBanned bool) (*Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return nil, ErrNotFound
	}
	if s := f.sanctions[p.AuthorID]; s != nil && s.Kind == SanctionBan && !includeBanned {
		return nil, ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) ListPosts(_ context.Context, q PostQuery) ([]*Post, error) {
	f.lastQuery = q
	return nil, nil
}

func (f *fakeRepo) DeletePost(_ context.Context, id, _ uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeRepo) CreateComment(_ context.Context, c *Comment) error {
	c.ID = uuid.New()
	f.comments[c.ID] = c
	return nil
}

func (f *fakeRepo) GetComment(_ context.Context, id uuid.UUID) (*Comment, error) {
	if c, ok := f.comments[id]; ok {
		return c, nil
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) ListComments(context.Context, uuid.UUID, bool) ([]*Comment, error) {
	return f.listed, nil
}

func (f *fakeRepo) DeleteComment(_ context.Context, id, _ uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeRepo) Vote(_ context.Context, _, postID uuid.UUID, value int) (int, error) {
	f.votes[postID] = value
	return value, nil
}

func (f *fakeRepo) CountSince(_ context.Context, action string, _ uuid.UUID, _ time.Time) (int, error) {
	return f.counts[action], nil
}

func (f *fakeRepo) Snapshot(_ context.Context, kind string, ref, owner uuid.UUID) (Snapshot, error) {
	o, ok := f.snapshots[ref]
	if !ok || (kind == KindItinerary && o != owner) {
		return Snapshot{}, ErrNotFound
	}
	return Snapshot{Title: "Lisbon in 3 days"}, nil
}

func (f *fakeRepo) ActiveSanction(_ context.Context, userID uuid.UUID) (*Sanction, error) {
	return f.sanctions[userID], nil
}

func (f *fakeRepo) CreateSanction(_ context.Context, s *Sanction) error {
	s.ID = uuid.New()
	f.sanctions[s.UserID] = s
	return nil
}

const adminEmail = "Admin@Loci.test"

func setup(t *testing.T) (*Service, *fakeRepo, Caller, Caller) {
	t.Helper()
	repo := newFakeRepo()
	svc := NewService(repo, []string{"", " admin@loci.test "}, DefaultLimits)
	admin := Caller{ID: uuid.New(), Email: adminEmail}
	user := Caller{ID: uuid.New(), Email: "someone@loci.test"}
	return svc, repo, admin, user
}

func mustBoard(t *testing.T, svc *Service, c Caller, slug string) *Board {
	t.Helper()
	b, err := svc.CreateBoard(context.Background(), c, slug, "Trip reports", "")
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	return b
}

func TestIsAdminMatchesListCaseInsensitively(t *testing.T) {
	svc, _, admin, user := setup(t)
	if !svc.IsAdmin(admin) {
		t.Error("listed email is not an admin")
	}
	if svc.IsAdmin(user) {
		t.Error("unlisted email is an admin")
	}
	if svc.IsAdmin(Caller{Email: adminEmail}) {
		t.Error("an anonymous caller carrying an admin email is an admin")
	}
}

func TestWritesNeedASession(t *testing.T) {
	svc, _, _, _ := setup(t)
	_, err := svc.CreateBoard(context.Background(), Caller{}, "lisbon", "Lisbon", "")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("got %v, want ErrUnauthenticated", err)
	}
}

func TestReservedSlugsAreTaken(t *testing.T) {
	svc, _, _, user := setup(t)
	for _, slug := range []string{"new", "admin", "all", "submit"} {
		if _, err := svc.CreateBoard(context.Background(), user, slug, "Some board", ""); !errors.Is(err, ErrSlugTaken) {
			t.Errorf("%s: got %v, want ErrSlugTaken", slug, err)
		}
	}
}

func TestMutedUserCannotWriteButAdminIsExempt(t *testing.T) {
	svc, repo, admin, user := setup(t)
	ctx := context.Background()
	b := mustBoard(t, svc, admin, "lisbon")
	until := time.Now().Add(time.Hour)
	if _, err := svc.SanctionUser(ctx, admin, user.ID, SanctionMute, "spam", &until); err != nil {
		t.Fatalf("SanctionUser: %v", err)
	}

	_, err := svc.CreatePost(ctx, user, PostInput{BoardSlug: b.Slug, Title: "Hello there"})
	var sanctioned *SanctionedError
	if !errors.As(err, &sanctioned) || !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want a SanctionedError wrapping ErrForbidden", err)
	}

	// An admin under a sanction row (made by hand, say) still moderates.
	repo.sanctions[admin.ID] = &Sanction{Kind: SanctionBan}
	if _, err := svc.CreatePost(ctx, admin, PostInput{BoardSlug: b.Slug, Title: "Admin post"}); err != nil {
		t.Fatalf("admin CreatePost: %v", err)
	}
}

func TestRateLimitPerAction(t *testing.T) {
	svc, repo, admin, user := setup(t)
	ctx := context.Background()
	b := mustBoard(t, svc, admin, "lisbon")
	repo.counts[ActionPost] = DefaultLimits.PostsPerHour
	if _, err := svc.CreatePost(ctx, user, PostInput{BoardSlug: b.Slug, Title: "One too many"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v, want ErrRateLimited", err)
	}
	// Admins are exempt.
	if _, err := svc.CreatePost(ctx, admin, PostInput{BoardSlug: b.Slug, Title: "Admin post"}); err != nil {
		t.Fatalf("admin CreatePost: %v", err)
	}
	// Other actions have their own budget.
	repo.counts[ActionComment] = 0
	p := firstPost(repo)
	if _, err := svc.CreateComment(ctx, user, p.ID, nil, "nice"); err != nil {
		t.Fatalf("CreateComment under the post limit: %v", err)
	}
}

func firstPost(repo *fakeRepo) *Post {
	for _, p := range repo.posts {
		return p
	}
	return nil
}

func TestPostValidation(t *testing.T) {
	svc, _, admin, user := setup(t)
	ctx := context.Background()
	b := mustBoard(t, svc, admin, "lisbon")
	cases := map[string]PostInput{
		"blank title":    {BoardSlug: b.Slug, Title: "   "},
		"javascript url": {BoardSlug: b.Slug, Title: "Look", URL: "javascript:alert(1)"},
		"relative url":   {BoardSlug: b.Slug, Title: "Look", URL: "/boards"},
		"bad attach ref": {BoardSlug: b.Slug, Title: "Look", AttachKind: KindPOI, AttachRef: "nope"},
	}
	for name, in := range cases {
		if _, err := svc.CreatePost(ctx, user, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if _, err := svc.CreatePost(ctx, user, PostInput{BoardSlug: "nowhere", Title: "Look"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown board: got %v, want ErrNotFound", err)
	}
}

func TestAttachOnlyYourOwnItinerary(t *testing.T) {
	svc, repo, admin, user := setup(t)
	ctx := context.Background()
	b := mustBoard(t, svc, admin, "lisbon")
	mine, theirs := uuid.New(), uuid.New()
	repo.snapshots[mine] = user.ID
	repo.snapshots[theirs] = admin.ID

	p, err := svc.CreatePost(ctx, user, PostInput{BoardSlug: b.Slug, Title: "My trip", AttachKind: KindItinerary, AttachRef: mine.String()})
	if err != nil {
		t.Fatalf("own itinerary: %v", err)
	}
	if p.Attachment == nil || p.Attachment.Snapshot.Title != "Lisbon in 3 days" {
		t.Fatalf("attachment not frozen: %+v", p.Attachment)
	}
	_, err = svc.CreatePost(ctx, user, PostInput{BoardSlug: b.Slug, Title: "Their trip", AttachKind: KindItinerary, AttachRef: theirs.String()})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("someone else's itinerary: got %v, want ErrInvalid", err)
	}
}

func TestDeletePostAuthorOrAdmin(t *testing.T) {
	svc, repo, admin, user := setup(t)
	ctx := context.Background()
	b := mustBoard(t, svc, admin, "lisbon")
	p, err := svc.CreatePost(ctx, user, PostInput{BoardSlug: b.Slug, Title: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	stranger := Caller{ID: uuid.New(), Email: "x@loci.test"}
	if err := svc.DeletePost(ctx, stranger, p.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger: got %v, want ErrForbidden", err)
	}
	if err := svc.DeletePost(ctx, user, p.ID); err != nil {
		t.Fatalf("author: %v", err)
	}
	// An admin can delete a banned author's post, which readers no longer see.
	repo.sanctions[user.ID] = &Sanction{Kind: SanctionBan}
	if err := svc.DeletePost(ctx, admin, p.ID); err != nil {
		t.Fatalf("admin on a banned author's post: %v", err)
	}
}

func TestDeleteBoardAdminOnly(t *testing.T) {
	svc, _, admin, user := setup(t)
	ctx := context.Background()
	b := mustBoard(t, svc, user, "lisbon")
	if err := svc.DeleteBoard(ctx, user, b.Slug); !errors.Is(err, ErrForbidden) {
		t.Fatalf("creator: got %v, want ErrForbidden", err)
	}
	if err := svc.DeleteBoard(ctx, admin, b.Slug); err != nil {
		t.Fatalf("admin: %v", err)
	}
}

func TestSanctionRules(t *testing.T) {
	svc, _, admin, user := setup(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	if _, err := svc.SanctionUser(ctx, user, admin.ID, SanctionBan, "", nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-admin: got %v, want ErrForbidden", err)
	}
	if _, err := svc.SanctionUser(ctx, admin, admin.ID, SanctionBan, "", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("self: got %v, want ErrInvalid", err)
	}
	if _, err := svc.SanctionUser(ctx, admin, user.ID, SanctionMute, "", &past); !errors.Is(err, ErrInvalid) {
		t.Errorf("already expired: got %v, want ErrInvalid", err)
	}
	if _, err := svc.SanctionUser(ctx, admin, user.ID, "kick", "", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown kind: got %v, want ErrInvalid", err)
	}
}

func TestVoteRejectsOutOfRangeAndSanctioned(t *testing.T) {
	svc, repo, admin, user := setup(t)
	ctx := context.Background()
	b := mustBoard(t, svc, admin, "lisbon")
	p, _ := svc.CreatePost(ctx, admin, PostInput{BoardSlug: b.Slug, Title: "Vote on me"})
	if _, err := svc.Vote(ctx, user, p.ID, 2); !errors.Is(err, ErrInvalid) {
		t.Errorf("value 2: got %v, want ErrInvalid", err)
	}
	if score, err := svc.Vote(ctx, user, p.ID, -1); err != nil || score != -1 {
		t.Errorf("downvote: score %d, err %v", score, err)
	}
	repo.sanctions[user.ID] = &Sanction{Kind: SanctionMute}
	if _, err := svc.Vote(ctx, user, p.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Errorf("muted: got %v, want ErrForbidden", err)
	}
}

func TestPruneDeletedKeepsPlaceholdersOnlyForLiveReplies(t *testing.T) {
	id := func() uuid.UUID { return uuid.New() }
	root, deadLeaf, deadParent, reply, deadChain, deadChainChild := id(), id(), id(), id(), id(), id()
	comments := []*Comment{
		{ID: root},
		{ID: deadLeaf, Deleted: true},
		{ID: deadParent, Deleted: true},
		{ID: reply, ParentID: &deadParent},
		{ID: deadChain, Deleted: true},
		{ID: deadChainChild, ParentID: &deadChain, Deleted: true},
	}
	got := pruneDeleted(comments)
	want := []uuid.UUID{root, deadParent, reply}
	if len(got) != len(want) {
		t.Fatalf("kept %d comments, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.ID != want[i] {
			t.Errorf("comment %d: got %s, want %s", i, c.ID, want[i])
		}
	}
}

func TestListPostsDefaultsAndCursors(t *testing.T) {
	svc, repo, admin, user := setup(t)
	ctx := context.Background()

	if _, _, err := svc.ListPosts(ctx, user, ListInput{}); err != nil {
		t.Fatal(err)
	}
	if repo.lastQuery.Sort != SortNew || repo.lastQuery.Limit != defaultPageSize || repo.lastQuery.IncludeBanned {
		t.Errorf("defaults: %+v", repo.lastQuery)
	}

	if _, _, err := svc.ListPosts(ctx, admin, ListInput{Sort: SortTop}); err != nil {
		t.Fatal(err)
	}
	q := repo.lastQuery
	if q.Since == nil || time.Since(*q.Since) < 6*24*time.Hour || !q.IncludeBanned {
		t.Errorf("top defaults to a week and admins see banned authors: %+v", q)
	}

	if _, _, err := svc.ListPosts(ctx, user, ListInput{Cursor: "garbage!"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad cursor: got %v, want ErrInvalid", err)
	}

	c := PostCursor{CreatedAt: time.Date(2026, 10, 1, 8, 0, 0, 123456000, time.UTC), ID: uuid.New()}
	back, err := decodeKeyset(encodeKeyset(c))
	if err != nil || !back.CreatedAt.Equal(c.CreatedAt) || back.ID != c.ID {
		t.Errorf("keyset round trip: %+v, %v", back, err)
	}
	if n, err := decodeOffset(encodeOffset(60)); err != nil || n != 60 {
		t.Errorf("offset round trip: %d, %v", n, err)
	}
}

func TestDomain(t *testing.T) {
	cases := map[string]string{
		"https://www.YouTube.com/watch?v=1": "youtube.com",
		"http://bitrig.com":                 "bitrig.com",
		"ftp://example.com":                 "",
		"not a url":                         "",
	}
	for in, want := range cases {
		if got := Domain(in); got != want {
			t.Errorf("Domain(%q) = %q, want %q", in, got, want)
		}
	}
}
