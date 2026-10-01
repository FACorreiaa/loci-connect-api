package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a board, post, comment or sanction does not
// exist, is deleted, or is hidden from the caller.
var ErrNotFound = errors.New("not found")

// ErrSlugTaken is returned when a live board already uses the slug.
var ErrSlugTaken = errors.New("a board with that address already exists")

// ErrInvalid is returned for input the proto rules cannot express.
var ErrInvalid = errors.New("invalid request")

const pgUniqueViolation = "23505"

// Attachment kinds, as stored in board_posts.attachment_kind.
const (
	KindItinerary = "itinerary"
	KindPOI       = "poi"
	KindCity      = "city"
)

// Sanction kinds, as stored in board_sanctions.kind.
const (
	SanctionMute = "mute"
	SanctionBan  = "ban"
)

type Board struct {
	ID             uuid.UUID
	Slug           string
	Name           string
	Description    string
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	PostCount      int
	LastActivityAt time.Time
}

// Snapshot is a Loci item frozen when it was attached to a post.
type Snapshot struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	ImageURL string `json:"image_url"`
	City     string `json:"city"`
}

type Attachment struct {
	Kind     string
	Ref      string
	Snapshot Snapshot
}

type Post struct {
	ID           uuid.UUID
	BoardID      uuid.UUID
	BoardSlug    string
	BoardName    string
	AuthorID     uuid.UUID
	Title        string
	URL          string
	Body         string
	Attachment   *Attachment
	Score        int
	CommentCount int
	// MyVote is per viewer, not stored on the row.
	MyVote    int
	CreatedAt time.Time
}

type Comment struct {
	ID       uuid.UUID
	PostID   uuid.UUID
	ParentID *uuid.UUID
	AuthorID uuid.UUID
	Body     string
	// Deleted covers both a deleted comment and one whose author is banned:
	// either way only its place in the thread is kept.
	Deleted   bool
	CreatedAt time.Time
}

type Sanction struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Kind      string
	Reason    string
	ExpiresAt *time.Time
	CreatedBy uuid.UUID
	CreatedAt time.Time
	LiftedAt  *time.Time
}

// Sort orders for ListPosts.
const (
	SortNew = "new"
	SortTop = "top"
)

// PostQuery selects a page of posts.
type PostQuery struct {
	// BoardID nil lists every board.
	BoardID *uuid.UUID
	Sort    string
	// Since bounds TOP to a window; nil is all time.
	Since *time.Time
	// After is the NEW keyset: the last post of the previous page.
	After *PostCursor
	// Offset pages TOP.
	Offset int
	Limit  int
	Viewer uuid.UUID
	// IncludeBanned shows banned authors' posts (to admins).
	IncludeBanned bool
}

type PostCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// Write actions counted for the per-user limits.
const (
	ActionBoard   = "board"
	ActionPost    = "post"
	ActionComment = "comment"
	ActionVote    = "vote"
)

type Repository interface {
	CreateBoard(ctx context.Context, b *Board) error
	GetBoard(ctx context.Context, slug string) (*Board, error)
	ListBoards(ctx context.Context, offset, limit int) ([]*Board, error)
	DeleteBoard(ctx context.Context, slug string, by uuid.UUID) error

	CreatePost(ctx context.Context, p *Post) error
	GetPost(ctx context.Context, id, viewer uuid.UUID, includeBanned bool) (*Post, error)
	ListPosts(ctx context.Context, q PostQuery) ([]*Post, error)
	DeletePost(ctx context.Context, id, by uuid.UUID) error

	CreateComment(ctx context.Context, c *Comment) error
	GetComment(ctx context.Context, id uuid.UUID) (*Comment, error)
	ListComments(ctx context.Context, postID uuid.UUID, includeBanned bool) ([]*Comment, error)
	DeleteComment(ctx context.Context, id, by uuid.UUID) error

	// Vote sets the user's vote (-1, 0 to clear, 1) and returns the post's
	// new score.
	Vote(ctx context.Context, userID, postID uuid.UUID, value int) (int, error)

	// CountSince counts the user's writes of one action since t.
	CountSince(ctx context.Context, action string, userID uuid.UUID, t time.Time) (int, error)

	// Snapshot freezes the item a post attaches. An itinerary must be the
	// owner's own; anything missing is ErrNotFound.
	Snapshot(ctx context.Context, kind string, ref, owner uuid.UUID) (Snapshot, error)

	// ActiveSanction is the user's strongest active sanction (a ban beats a
	// mute, then the later expiry wins), or nil.
	ActiveSanction(ctx context.Context, userID uuid.UUID) (*Sanction, error)
	CreateSanction(ctx context.Context, s *Sanction) error
	LiftSanction(ctx context.Context, id uuid.UUID) error
	ListSanctions(ctx context.Context, activeOnly bool) ([]*Sanction, error)
}

type repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) Repository {
	return &repository{db: db}
}

// activeSanction is the SQL predicate for a live sanction row aliased s.
const activeSanction = `s.lifted_at IS NULL AND (s.expires_at IS NULL OR s.expires_at > NOW())`

// notBanned hides rows whose author (the column named) is banned.
func notBanned(authorCol string) string {
	return `NOT EXISTS (SELECT 1 FROM board_sanctions s WHERE s.user_id = ` + authorCol +
		` AND s.kind = 'ban' AND ` + activeSanction + `)`
}

func (r *repository) CreateBoard(ctx context.Context, b *Board) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO boards (slug, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`,
		b.Slug, b.Name, b.Description, b.CreatedBy).Scan(&b.ID, &b.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return ErrSlugTaken
	}
	if err != nil {
		return fmt.Errorf("insert board: %w", err)
	}
	b.LastActivityAt = b.CreatedAt
	return nil
}

// boardColumns reads a board with its live post count and last activity.
// Banned authors' posts still count: the numbers are not worth a join.
const boardColumns = `
	b.id, b.slug::text, b.name, b.description, b.created_by, b.created_at,
	(SELECT COUNT(*) FROM board_posts p WHERE p.board_id = b.id AND p.deleted_at IS NULL),
	COALESCE((SELECT MAX(p.created_at) FROM board_posts p WHERE p.board_id = b.id AND p.deleted_at IS NULL), b.created_at)`

func scanBoard(row pgx.Row) (*Board, error) {
	var b Board
	err := row.Scan(&b.ID, &b.Slug, &b.Name, &b.Description, &b.CreatedBy, &b.CreatedAt, &b.PostCount, &b.LastActivityAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *repository) GetBoard(ctx context.Context, slug string) (*Board, error) {
	b, err := scanBoard(r.db.QueryRow(ctx, `SELECT `+boardColumns+`
		FROM boards b WHERE b.slug = $1 AND b.deleted_at IS NULL`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get board: %w", err)
	}
	return b, nil
}

func (r *repository) ListBoards(ctx context.Context, offset, limit int) ([]*Board, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM (SELECT `+boardColumns+`
		FROM boards b WHERE b.deleted_at IS NULL) AS t
		ORDER BY 8 DESC, 1 DESC
		OFFSET $1 LIMIT $2`, offset, limit)
	if err != nil {
		return nil, fmt.Errorf("list boards: %w", err)
	}
	defer rows.Close()
	var out []*Board
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, fmt.Errorf("scan board: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *repository) DeleteBoard(ctx context.Context, slug string, by uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE boards SET deleted_at = NOW(), deleted_by = $2
		WHERE slug = $1 AND deleted_at IS NULL`, slug, by)
	if err != nil {
		return fmt.Errorf("delete board: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) CreatePost(ctx context.Context, p *Post) error {
	var kind, ref *string
	var snap []byte
	if p.Attachment != nil {
		kind, ref = &p.Attachment.Kind, &p.Attachment.Ref
		var err error
		if snap, err = json.Marshal(p.Attachment.Snapshot); err != nil {
			return fmt.Errorf("encode snapshot: %w", err)
		}
	}
	err := r.db.QueryRow(ctx, `
		INSERT INTO board_posts (board_id, author_id, title, url, body, attachment_kind, attachment_ref, attachment_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`,
		p.BoardID, p.AuthorID, p.Title, p.URL, p.Body, kind, ref, snap).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert post: %w", err)
	}
	return nil
}

// postColumns reads a post for viewer $1.
const postColumns = `
	p.id, p.board_id, b.slug::text, b.name, p.author_id, p.title, p.url, p.body,
	p.attachment_kind, p.attachment_ref, p.attachment_snapshot,
	p.score, p.comment_count,
	COALESCE((SELECT v.value FROM board_post_votes v WHERE v.post_id = p.id AND v.user_id = $1), 0),
	p.created_at`

func scanPost(row pgx.Row) (*Post, error) {
	var (
		p         Post
		kind, ref *string
		snap      []byte
		myVote    int16
	)
	err := row.Scan(&p.ID, &p.BoardID, &p.BoardSlug, &p.BoardName, &p.AuthorID, &p.Title, &p.URL, &p.Body,
		&kind, &ref, &snap, &p.Score, &p.CommentCount, &myVote, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.MyVote = int(myVote)
	if kind != nil && ref != nil {
		a := &Attachment{Kind: *kind, Ref: *ref}
		if len(snap) > 0 {
			if err := json.Unmarshal(snap, &a.Snapshot); err != nil {
				return nil, fmt.Errorf("decode snapshot: %w", err)
			}
		}
		p.Attachment = a
	}
	return &p, nil
}

func (r *repository) GetPost(ctx context.Context, id, viewer uuid.UUID, includeBanned bool) (*Post, error) {
	q := `SELECT ` + postColumns + `
		FROM board_posts p JOIN boards b ON b.id = p.board_id
		WHERE p.id = $2 AND p.deleted_at IS NULL AND b.deleted_at IS NULL`
	if !includeBanned {
		q += ` AND ` + notBanned("p.author_id")
	}
	p, err := scanPost(r.db.QueryRow(ctx, q, viewer, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get post: %w", err)
	}
	return p, nil
}

func (r *repository) ListPosts(ctx context.Context, q PostQuery) ([]*Post, error) {
	args := []any{q.Viewer}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	where := `p.deleted_at IS NULL AND b.deleted_at IS NULL`
	if !q.IncludeBanned {
		where += ` AND ` + notBanned("p.author_id")
	}
	if q.BoardID != nil {
		where += ` AND p.board_id = ` + arg(*q.BoardID)
	}
	var order, page string
	switch q.Sort {
	case SortTop:
		if q.Since != nil {
			where += ` AND p.created_at >= ` + arg(*q.Since)
		}
		order = `p.score DESC, p.created_at DESC, p.id DESC`
		page = ` OFFSET ` + arg(q.Offset)
	default:
		if q.After != nil {
			where += fmt.Sprintf(` AND (p.created_at, p.id) < (%s, %s)`, arg(q.After.CreatedAt), arg(q.After.ID))
		}
		order = `p.created_at DESC, p.id DESC`
	}
	sql := `SELECT ` + postColumns + `
		FROM board_posts p JOIN boards b ON b.id = p.board_id
		WHERE ` + where + `
		ORDER BY ` + order + page + ` LIMIT ` + arg(q.Limit)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list posts: %w", err)
	}
	defer rows.Close()
	var out []*Post
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *repository) DeletePost(ctx context.Context, id, by uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE board_posts SET deleted_at = NOW(), deleted_by = $2
		WHERE id = $1 AND deleted_at IS NULL`, id, by)
	if err != nil {
		return fmt.Errorf("delete post: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) CreateComment(ctx context.Context, c *Comment) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// The parent must be a comment on the same post; a reply to a
		// deleted comment is still allowed, as on most forums.
		if c.ParentID != nil {
			var ok bool
			err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM board_comments WHERE id = $1 AND post_id = $2)`,
				*c.ParentID, c.PostID).Scan(&ok)
			if err != nil {
				return fmt.Errorf("check parent: %w", err)
			}
			if !ok {
				return fmt.Errorf("%w: parent comment is not on this post", ErrInvalid)
			}
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO board_comments (post_id, parent_id, author_id, body)
			VALUES ($1, $2, $3, $4)
			RETURNING id, created_at`,
			c.PostID, c.ParentID, c.AuthorID, c.Body).Scan(&c.ID, &c.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert comment: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE board_posts SET comment_count = comment_count + 1 WHERE id = $1`, c.PostID)
		if err != nil {
			return fmt.Errorf("count comment: %w", err)
		}
		return nil
	})
}

func (r *repository) GetComment(ctx context.Context, id uuid.UUID) (*Comment, error) {
	var c Comment
	var deletedAt *time.Time
	err := r.db.QueryRow(ctx, `
		SELECT id, post_id, parent_id, author_id, body, deleted_at, created_at
		FROM board_comments WHERE id = $1`, id).
		Scan(&c.ID, &c.PostID, &c.ParentID, &c.AuthorID, &c.Body, &deletedAt, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get comment: %w", err)
	}
	c.Deleted = deletedAt != nil
	return &c, nil
}

func (r *repository) ListComments(ctx context.Context, postID uuid.UUID, includeBanned bool) ([]*Comment, error) {
	hidden := `c.deleted_at IS NOT NULL`
	if !includeBanned {
		hidden += ` OR NOT ` + notBanned("c.author_id")
	}
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.post_id, c.parent_id, c.author_id, c.body, (`+hidden+`), c.created_at
		FROM board_comments c
		WHERE c.post_id = $1
		ORDER BY c.created_at, c.id`, postID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.PostID, &c.ParentID, &c.AuthorID, &c.Body, &c.Deleted, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan comment: %w", err)
		}
		if c.Deleted {
			c.Body = ""
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *repository) DeleteComment(ctx context.Context, id, by uuid.UUID) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var postID uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE board_comments SET deleted_at = NOW(), deleted_by = $2
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING post_id`, id, by).Scan(&postID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete comment: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE board_posts SET comment_count = GREATEST(comment_count - 1, 0) WHERE id = $1`, postID)
		if err != nil {
			return fmt.Errorf("uncount comment: %w", err)
		}
		return nil
	})
}

func (r *repository) Vote(ctx context.Context, userID, postID uuid.UUID, value int) (int, error) {
	var score int
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// Lock the post so concurrent votes apply their deltas one at a time.
		err := tx.QueryRow(ctx, `SELECT score FROM board_posts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, postID).Scan(&score)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock post: %w", err)
		}
		var old int16
		err = tx.QueryRow(ctx, `SELECT value FROM board_post_votes WHERE user_id = $1 AND post_id = $2`, userID, postID).Scan(&old)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read vote: %w", err)
		}
		if int(old) == value {
			return nil
		}
		if value == 0 {
			_, err = tx.Exec(ctx, `DELETE FROM board_post_votes WHERE user_id = $1 AND post_id = $2`, userID, postID)
		} else {
			_, err = tx.Exec(ctx, `
				INSERT INTO board_post_votes (user_id, post_id, value) VALUES ($1, $2, $3)
				ON CONFLICT (user_id, post_id) DO UPDATE SET value = EXCLUDED.value, created_at = NOW()`,
				userID, postID, value)
		}
		if err != nil {
			return fmt.Errorf("write vote: %w", err)
		}
		up := boolInt(value == 1) - boolInt(old == 1)
		down := boolInt(value == -1) - boolInt(old == -1)
		return tx.QueryRow(ctx, `
			UPDATE board_posts SET upvotes = upvotes + $2, downvotes = downvotes + $3, score = score + $2 - $3
			WHERE id = $1 RETURNING score`, postID, up, down).Scan(&score)
	})
	if err != nil {
		return 0, err
	}
	return score, nil
}

// truncate cuts s to at most n runes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *repository) CountSince(ctx context.Context, action string, userID uuid.UUID, t time.Time) (int, error) {
	var q string
	switch action {
	case ActionBoard:
		q = `SELECT COUNT(*) FROM boards WHERE created_by = $1 AND created_at >= $2`
	case ActionPost:
		q = `SELECT COUNT(*) FROM board_posts WHERE author_id = $1 AND created_at >= $2`
	case ActionComment:
		q = `SELECT COUNT(*) FROM board_comments WHERE author_id = $1 AND created_at >= $2`
	case ActionVote:
		q = `SELECT COUNT(*) FROM board_post_votes WHERE user_id = $1 AND created_at >= $2`
	default:
		return 0, fmt.Errorf("unknown action %q", action)
	}
	var n int
	if err := r.db.QueryRow(ctx, q, userID, t).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s: %w", action, err)
	}
	return n, nil
}

func (r *repository) Snapshot(ctx context.Context, kind string, ref, owner uuid.UUID) (Snapshot, error) {
	var (
		s   Snapshot
		img *string
		err error
	)
	switch kind {
	case KindItinerary:
		err = r.db.QueryRow(ctx, `
			SELECT i.title, COALESCE(i.description, ''), NULL::text, COALESCE(c.name, '')
			FROM user_saved_itineraries i LEFT JOIN cities c ON c.id = i.primary_city_id
			WHERE i.id = $1 AND i.user_id = $2`, ref, owner).Scan(&s.Title, &s.Subtitle, &img, &s.City)
	case KindPOI:
		err = r.db.QueryRow(ctx, `
			SELECT p.name, COALESCE(p.category, ''),
			       (SELECT url FROM poi_images WHERE poi_id = p.id ORDER BY position, fetched_at LIMIT 1),
			       COALESCE(c.name, '')
			FROM points_of_interest p LEFT JOIN cities c ON c.id = p.city_id
			WHERE p.id = $1`, ref).Scan(&s.Title, &s.Subtitle, &img, &s.City)
	case KindCity:
		err = r.db.QueryRow(ctx, `SELECT name, country, NULL::text, name FROM cities WHERE id = $1`, ref).
			Scan(&s.Title, &s.Subtitle, &img, &s.City)
	default:
		return s, fmt.Errorf("%w: unknown attachment kind", ErrInvalid)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, fmt.Errorf("snapshot %s: %w", kind, err)
	}
	if img != nil {
		s.ImageURL = *img
	}
	s.Title = truncate(s.Title, 300)
	s.Subtitle = truncate(s.Subtitle, 300)
	return s, nil
}

const sanctionColumns = `s.id, s.user_id, s.kind, s.reason, s.expires_at, COALESCE(s.created_by, '00000000-0000-0000-0000-000000000000'), s.created_at, s.lifted_at`

func scanSanction(row pgx.Row) (*Sanction, error) {
	var s Sanction
	err := row.Scan(&s.ID, &s.UserID, &s.Kind, &s.Reason, &s.ExpiresAt, &s.CreatedBy, &s.CreatedAt, &s.LiftedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *repository) ActiveSanction(ctx context.Context, userID uuid.UUID) (*Sanction, error) {
	s, err := scanSanction(r.db.QueryRow(ctx, `SELECT `+sanctionColumns+`
		FROM board_sanctions s
		WHERE s.user_id = $1 AND `+activeSanction+`
		ORDER BY (s.kind = 'ban') DESC, s.expires_at DESC NULLS FIRST
		LIMIT 1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active sanction: %w", err)
	}
	return s, nil
}

func (r *repository) CreateSanction(ctx context.Context, s *Sanction) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO board_sanctions (user_id, kind, reason, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`,
		s.UserID, s.Kind, s.Reason, s.ExpiresAt, s.CreatedBy).Scan(&s.ID, &s.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("insert sanction: %w", err)
	}
	return nil
}

func (r *repository) LiftSanction(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `UPDATE board_sanctions SET lifted_at = NOW() WHERE id = $1 AND lifted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("lift sanction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) ListSanctions(ctx context.Context, activeOnly bool) ([]*Sanction, error) {
	q := `SELECT ` + sanctionColumns + ` FROM board_sanctions s`
	if activeOnly {
		q += ` WHERE ` + activeSanction
	}
	q += ` ORDER BY s.created_at DESC LIMIT 500`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list sanctions: %w", err)
	}
	defer rows.Close()
	var out []*Sanction
	for rows.Next() {
		s, err := scanSanction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan sanction: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
