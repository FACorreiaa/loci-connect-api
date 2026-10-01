package boards

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrUnauthenticated is returned for a write without a session.
var ErrUnauthenticated = errors.New("sign in to do that")

// ErrForbidden is returned when the caller may not do this: not the author,
// not an admin, or under a sanction (see SanctionedError).
var ErrForbidden = errors.New("not allowed")

// ErrRateLimited is returned when the caller is over a write limit.
var ErrRateLimited = errors.New("slow down: too many of those recently")

// SanctionedError is a write refused because the caller is muted or banned.
type SanctionedError struct{ Sanction *Sanction }

func (e *SanctionedError) Error() string {
	verb := "muted"
	if e.Sanction.Kind == SanctionBan {
		verb = "banned"
	}
	if e.Sanction.ExpiresAt == nil {
		return "you are " + verb + " from boards"
	}
	return fmt.Sprintf("you are %s from boards until %s", verb, e.Sanction.ExpiresAt.UTC().Format("2 Jan 2006 15:04 MST"))
}

func (e *SanctionedError) Unwrap() error { return ErrForbidden }

// Caller is who is asking. A zero ID is an anonymous reader.
type Caller struct {
	ID    uuid.UUID
	Email string
}

func (c Caller) SignedIn() bool { return c.ID != uuid.Nil }

// Limits cap each user's writes. Admins are exempt.
type Limits struct {
	BoardsPerDay    int
	PostsPerHour    int
	CommentsPerHour int
	VotesPerHour    int
}

var DefaultLimits = Limits{BoardsPerDay: 3, PostsPerHour: 10, CommentsPerHour: 60, VotesPerHour: 300}

const (
	defaultPageSize = 30
	maxPageSize     = 100
)

// reservedSlugs collide with the web client's /boards/<word> routes.
var reservedSlugs = map[string]bool{"new": true, "admin": true, "all": true, "submit": true}

type Service struct {
	repo   Repository
	admins map[string]struct{}
	limits Limits
	now    func() time.Time
}

// NewService builds the boards service. adminEmails moderate every board.
func NewService(repo Repository, adminEmails []string, limits Limits) *Service {
	admins := make(map[string]struct{}, len(adminEmails))
	for _, e := range adminEmails {
		if e = normalizeEmail(e); e != "" {
			admins[e] = struct{}{}
		}
	}
	return &Service{repo: repo, admins: admins, limits: limits, now: time.Now}
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func (s *Service) IsAdmin(c Caller) bool {
	if !c.SignedIn() || c.Email == "" {
		return false
	}
	_, ok := s.admins[normalizeEmail(c.Email)]
	return ok
}

// Viewer reports what the caller may do: admin controls, and the sanction a
// muted or banned caller is under.
func (s *Service) Viewer(ctx context.Context, c Caller) (isAdmin bool, active *Sanction, err error) {
	if !c.SignedIn() {
		return false, nil, nil
	}
	active, err = s.repo.ActiveSanction(ctx, c.ID)
	return s.IsAdmin(c), active, err
}

// canWrite refuses anonymous, sanctioned and over-limit callers. Admins skip
// the sanction and limit checks.
func (s *Service) canWrite(ctx context.Context, c Caller, action string) error {
	if !c.SignedIn() {
		return ErrUnauthenticated
	}
	if s.IsAdmin(c) {
		return nil
	}
	sanction, err := s.repo.ActiveSanction(ctx, c.ID)
	if err != nil {
		return err
	}
	if sanction != nil {
		return &SanctionedError{Sanction: sanction}
	}
	limit, window := s.limitFor(action)
	if limit <= 0 {
		return nil
	}
	n, err := s.repo.CountSince(ctx, action, c.ID, s.now().Add(-window))
	if err != nil {
		return err
	}
	if n >= limit {
		return ErrRateLimited
	}
	return nil
}

func (s *Service) limitFor(action string) (int, time.Duration) {
	switch action {
	case ActionBoard:
		return s.limits.BoardsPerDay, 24 * time.Hour
	case ActionPost:
		return s.limits.PostsPerHour, time.Hour
	case ActionComment:
		return s.limits.CommentsPerHour, time.Hour
	case ActionVote:
		return s.limits.VotesPerHour, time.Hour
	}
	return 0, 0
}

func (s *Service) requireAdmin(c Caller) error {
	if !c.SignedIn() {
		return ErrUnauthenticated
	}
	if !s.IsAdmin(c) {
		return ErrForbidden
	}
	return nil
}

func pageSize(n int) int {
	if n <= 0 {
		return defaultPageSize
	}
	return min(n, maxPageSize)
}

// ListBoards pages boards by latest activity. The cursor is an offset.
func (s *Service) ListBoards(ctx context.Context, cursor string, size int) ([]*Board, string, error) {
	offset, err := decodeOffset(cursor)
	if err != nil {
		return nil, "", err
	}
	size = pageSize(size)
	boards, err := s.repo.ListBoards(ctx, offset, size)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(boards) == size {
		next = encodeOffset(offset + size)
	}
	return boards, next, nil
}

func (s *Service) GetBoard(ctx context.Context, slug string) (*Board, error) {
	return s.repo.GetBoard(ctx, strings.ToLower(slug))
}

func (s *Service) CreateBoard(ctx context.Context, c Caller, slug, name, description string) (*Board, error) {
	if err := s.canWrite(ctx, c, ActionBoard); err != nil {
		return nil, err
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if reservedSlugs[slug] {
		return nil, ErrSlugTaken
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 3 {
		return nil, fmt.Errorf("%w: a board needs a name of at least 3 characters", ErrInvalid)
	}
	b := &Board{Slug: slug, Name: name, Description: strings.TrimSpace(description), CreatedBy: c.ID}
	if err := s.repo.CreateBoard(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) DeleteBoard(ctx context.Context, c Caller, slug string) error {
	if err := s.requireAdmin(c); err != nil {
		return err
	}
	return s.repo.DeleteBoard(ctx, strings.ToLower(slug), c.ID)
}

// PostInput is a new post. AttachKind empty means no attachment.
type PostInput struct {
	BoardSlug  string
	Title      string
	URL        string
	Body       string
	AttachKind string
	AttachRef  string
}

func (s *Service) CreatePost(ctx context.Context, c Caller, in PostInput) (*Post, error) {
	if err := s.canWrite(ctx, c, ActionPost); err != nil {
		return nil, err
	}
	board, err := s.repo.GetBoard(ctx, strings.ToLower(in.BoardSlug))
	if err != nil {
		return nil, err
	}
	p := &Post{
		BoardID:   board.ID,
		BoardSlug: board.Slug,
		BoardName: board.Name,
		AuthorID:  c.ID,
		Title:     strings.TrimSpace(in.Title),
		URL:       strings.TrimSpace(in.URL),
		Body:      strings.TrimSpace(in.Body),
	}
	if len([]rune(p.Title)) < 3 {
		return nil, fmt.Errorf("%w: a post needs a title of at least 3 characters", ErrInvalid)
	}
	if p.URL != "" && Domain(p.URL) == "" {
		return nil, fmt.Errorf("%w: the link must be an http or https address", ErrInvalid)
	}
	if in.AttachKind != "" {
		ref, err := uuid.Parse(in.AttachRef)
		if err != nil {
			return nil, fmt.Errorf("%w: unknown attachment", ErrInvalid)
		}
		snap, err := s.repo.Snapshot(ctx, in.AttachKind, ref, c.ID)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: that item is not yours to share or no longer exists", ErrInvalid)
		}
		if err != nil {
			return nil, err
		}
		p.Attachment = &Attachment{Kind: in.AttachKind, Ref: ref.String(), Snapshot: snap}
	}
	if err := s.repo.CreatePost(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Domain is the hostname of an http(s) link without "www.", or "" when raw
// is not one.
func Domain(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// Windows for the TOP sort.
const (
	WindowDay  = "day"
	WindowWeek = "week"
	WindowAll  = "all"
)

type ListInput struct {
	// BoardSlug empty lists every board.
	BoardSlug string
	Sort      string
	Window    string
	Cursor    string
	PageSize  int
}

func (s *Service) ListPosts(ctx context.Context, c Caller, in ListInput) ([]*Post, string, error) {
	q := PostQuery{Sort: in.Sort, Limit: pageSize(in.PageSize), Viewer: c.ID, IncludeBanned: s.IsAdmin(c)}
	if q.Sort != SortTop {
		q.Sort = SortNew
	}
	if in.BoardSlug != "" {
		board, err := s.repo.GetBoard(ctx, strings.ToLower(in.BoardSlug))
		if err != nil {
			return nil, "", err
		}
		q.BoardID = &board.ID
	}
	var err error
	if q.Sort == SortTop {
		switch in.Window {
		case WindowAll:
		case WindowDay:
			since := s.now().Add(-24 * time.Hour)
			q.Since = &since
		default:
			since := s.now().Add(-7 * 24 * time.Hour)
			q.Since = &since
		}
		if q.Offset, err = decodeOffset(in.Cursor); err != nil {
			return nil, "", err
		}
	} else if q.After, err = decodeKeyset(in.Cursor); err != nil {
		return nil, "", err
	}
	posts, err := s.repo.ListPosts(ctx, q)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(posts) == q.Limit {
		if q.Sort == SortTop {
			next = encodeOffset(q.Offset + q.Limit)
		} else {
			last := posts[len(posts)-1]
			next = encodeKeyset(PostCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		}
	}
	return posts, next, nil
}

// GetPost returns a post and its comments in thread order. Deleted comments
// stay only as placeholders for replies that are still visible.
func (s *Service) GetPost(ctx context.Context, c Caller, id uuid.UUID) (*Post, []*Comment, error) {
	admin := s.IsAdmin(c)
	p, err := s.repo.GetPost(ctx, id, c.ID, admin)
	if err != nil {
		return nil, nil, err
	}
	comments, err := s.repo.ListComments(ctx, id, admin)
	if err != nil {
		return nil, nil, err
	}
	return p, pruneDeleted(comments), nil
}

// pruneDeleted drops deleted comments that have no visible reply below them.
// comments must be in creation order, so every reply follows its parent.
func pruneDeleted(comments []*Comment) []*Comment {
	keep := make([]bool, len(comments))
	liveChild := make(map[uuid.UUID]bool)
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		keep[i] = !c.Deleted || liveChild[c.ID]
		if keep[i] && c.ParentID != nil {
			liveChild[*c.ParentID] = true
		}
	}
	out := comments[:0:0]
	for i, c := range comments {
		if keep[i] {
			out = append(out, c)
		}
	}
	return out
}

func (s *Service) DeletePost(ctx context.Context, c Caller, id uuid.UUID) error {
	if !c.SignedIn() {
		return ErrUnauthenticated
	}
	p, err := s.repo.GetPost(ctx, id, c.ID, true)
	if err != nil {
		return err
	}
	if p.AuthorID != c.ID && !s.IsAdmin(c) {
		return ErrForbidden
	}
	return s.repo.DeletePost(ctx, id, c.ID)
}

func (s *Service) CreateComment(ctx context.Context, c Caller, postID uuid.UUID, parentID *uuid.UUID, body string) (*Comment, error) {
	if err := s.canWrite(ctx, c, ActionComment); err != nil {
		return nil, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("%w: a comment cannot be empty", ErrInvalid)
	}
	if _, err := s.repo.GetPost(ctx, postID, c.ID, false); err != nil {
		return nil, err
	}
	cm := &Comment{PostID: postID, ParentID: parentID, AuthorID: c.ID, Body: body}
	if err := s.repo.CreateComment(ctx, cm); err != nil {
		return nil, err
	}
	return cm, nil
}

func (s *Service) DeleteComment(ctx context.Context, c Caller, id uuid.UUID) error {
	if !c.SignedIn() {
		return ErrUnauthenticated
	}
	cm, err := s.repo.GetComment(ctx, id)
	if err != nil {
		return err
	}
	if cm.Deleted {
		return ErrNotFound
	}
	if cm.AuthorID != c.ID && !s.IsAdmin(c) {
		return ErrForbidden
	}
	return s.repo.DeleteComment(ctx, id, c.ID)
}

// Vote sets the caller's vote on a post and returns its new score.
func (s *Service) Vote(ctx context.Context, c Caller, postID uuid.UUID, value int) (int, error) {
	if value < -1 || value > 1 {
		return 0, fmt.Errorf("%w: a vote is -1, 0 or 1", ErrInvalid)
	}
	if err := s.canWrite(ctx, c, ActionVote); err != nil {
		return 0, err
	}
	if _, err := s.repo.GetPost(ctx, postID, c.ID, false); err != nil {
		return 0, err
	}
	return s.repo.Vote(ctx, c.ID, postID, value)
}

func (s *Service) SanctionUser(ctx context.Context, c Caller, userID uuid.UUID, kind, reason string, expiresAt *time.Time) (*Sanction, error) {
	if err := s.requireAdmin(c); err != nil {
		return nil, err
	}
	if kind != SanctionMute && kind != SanctionBan {
		return nil, fmt.Errorf("%w: a sanction is a mute or a ban", ErrInvalid)
	}
	if userID == c.ID {
		return nil, fmt.Errorf("%w: you cannot sanction yourself", ErrInvalid)
	}
	if expiresAt != nil && !expiresAt.After(s.now()) {
		return nil, fmt.Errorf("%w: the sanction would already be over", ErrInvalid)
	}
	sn := &Sanction{UserID: userID, Kind: kind, Reason: strings.TrimSpace(reason), ExpiresAt: expiresAt, CreatedBy: c.ID}
	if err := s.repo.CreateSanction(ctx, sn); err != nil {
		return nil, err
	}
	return sn, nil
}

func (s *Service) LiftSanction(ctx context.Context, c Caller, id uuid.UUID) error {
	if err := s.requireAdmin(c); err != nil {
		return err
	}
	return s.repo.LiftSanction(ctx, id)
}

func (s *Service) ListSanctions(ctx context.Context, c Caller, activeOnly bool) ([]*Sanction, error) {
	if err := s.requireAdmin(c); err != nil {
		return nil, err
	}
	return s.repo.ListSanctions(ctx, activeOnly)
}

// Cursors are opaque to clients: an offset ("o<n>") or a NEW keyset
// ("k<unix nanos>_<post id>"), base64url-encoded.

var errBadCursor = fmt.Errorf("%w: bad cursor", ErrInvalid)

func encodeOffset(n int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o" + strconv.Itoa(n)))
}

func decodeOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) < 2 || raw[0] != 'o' {
		return 0, errBadCursor
	}
	n, err := strconv.Atoi(string(raw[1:]))
	if err != nil || n < 0 {
		return 0, errBadCursor
	}
	return n, nil
}

func encodeKeyset(c PostCursor) string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "k%d_%s", c.CreatedAt.UnixNano(), c.ID))
}

func decodeKeyset(cursor string) (*PostCursor, error) {
	if cursor == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) < 2 || raw[0] != 'k' {
		return nil, errBadCursor
	}
	nanos, id, ok := strings.Cut(string(raw[1:]), "_")
	if !ok {
		return nil, errBadCursor
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return nil, errBadCursor
	}
	pid, err := uuid.Parse(id)
	if err != nil {
		return nil, errBadCursor
	}
	return &PostCursor{CreatedAt: time.Unix(0, n), ID: pid}, nil
}
