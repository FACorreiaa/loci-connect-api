// Package social is Loci's friends layer: mutual friendships, friend
// requests, invite links, blocks, contact matching and public profiles.
//
// Friendship is mutual on purpose. "Friends" trip visibility, and later
// location sharing, are seen only by people both sides approved.
package social

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound covers a user, request or invite that does not exist, and
	// one the caller may not see (a user who blocked them).
	ErrNotFound = errors.New("not found")
	// ErrConflict is a request that already exists or a state that no
	// longer allows the action.
	ErrConflict = errors.New("conflict")
)

// Relation is the caller's relation to another user (Relationship in
// social.proto).
type Relation int32

const (
	RelationNone      Relation = 1
	RelationRequested Relation = 2
	RelationIncoming  Relation = 3
	RelationFriends   Relation = 4
	RelationBlocked   Relation = 5
	RelationSelf      Relation = 6
)

// Friend is one friendship as seen by one side.
type Friend struct {
	User  *socialv1.PublicUser
	Since time.Time
}

// Request is a friend request with both sides' cards.
type Request struct {
	ID        uuid.UUID
	From, To  *socialv1.PublicUser
	CreatedAt time.Time
}

// Invite is a user's invite code.
type Invite struct {
	UserID uuid.UUID
	Code   string
	// ExpiresAt nil: the code never expires.
	ExpiresAt *time.Time
}

// Stats are the counts a profile shows.
type Stats struct {
	Cities, Countries, VisibleTrips, Friends int32
}

// Repository is the social store.
type Repository interface {
	PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error)
	UserIDByUsername(ctx context.Context, username string) (uuid.UUID, error)
	MemberSince(ctx context.Context, id uuid.UUID) (time.Time, error)

	// Relation reports friendship and a block in either direction.
	Relation(ctx context.Context, a, b uuid.UUID) (friends, blocked bool, err error)
	// RelationOf is viewer's relation to other. blockedBy reports that other
	// blocked viewer, which callers turn into NotFound.
	RelationOf(ctx context.Context, viewer, other uuid.UUID) (rel Relation, blockedBy bool, err error)
	FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	ListFriends(ctx context.Context, userID uuid.UUID) ([]Friend, error)
	RemoveFriend(ctx context.Context, a, b uuid.UUID) error

	// SendRequest records a pending request, or, when to had already asked
	// from, accepts that one instead (friends is then true).
	SendRequest(ctx context.Context, from, to uuid.UUID) (id uuid.UUID, friends bool, err error)
	// RespondRequest answers a pending request addressed to `to`; it returns
	// the sender.
	RespondRequest(ctx context.Context, id, to uuid.UUID, accept bool) (from uuid.UUID, err error)
	CancelRequest(ctx context.Context, id, from uuid.UUID) error
	ListRequests(ctx context.Context, userID uuid.UUID, incoming bool) ([]Request, error)
	CountRequestsSince(ctx context.Context, from uuid.UUID, since time.Time) (int, error)

	// MakeFriends makes a and b friends at once (an accepted invite),
	// closing any pending request between them.
	MakeFriends(ctx context.Context, a, b uuid.UUID) error

	Block(ctx context.Context, blocker, blocked uuid.UUID) error
	Unblock(ctx context.Context, blocker, blocked uuid.UUID) error

	InviteFor(ctx context.Context, userID uuid.UUID) (*Invite, error)
	SaveInvite(ctx context.Context, inv Invite) error
	InviteByCode(ctx context.Context, code string) (*Invite, error)
	// SetInvitedBy records who invited an account. It writes once: an account
	// that already has an inviter keeps it.
	SetInvitedBy(ctx context.Context, invitee, inviter uuid.UUID) (bool, error)

	// MatchHashes maps each hash that names a verified, active user's phone
	// or email to that user.
	MatchHashes(ctx context.Context, hashes []string) (map[string]uuid.UUID, error)
	// FacebookFriends is the active users whose linked Facebook account is
	// among the friends userID's own link granted. linked is false when
	// userID has not linked Facebook.
	FacebookFriends(ctx context.Context, userID uuid.UUID) (ids []uuid.UUID, linked bool, err error)
	SearchUsernames(ctx context.Context, prefix string, limit int) ([]uuid.UUID, error)
	Stats(ctx context.Context, owner uuid.UUID, tripVisibilities []int32) (Stats, error)
}

type repository struct {
	db *pgxpool.Pool
}

// NewRepository is the Postgres Repository.
func NewRepository(db *pgxpool.Pool) Repository { return &repository{db: db} }

func (r *repository) PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	out := make(map[uuid.UUID]*socialv1.PublicUser, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, COALESCE(username::text, ''), COALESCE(display_name, ''),
		       COALESCE(avatar_url, profile_image_url, ''), COALESCE(city, '')
		FROM users WHERE id = ANY($1) AND is_active`, ids)
	if err != nil {
		return nil, fmt.Errorf("public users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id uuid.UUID
			u  socialv1.PublicUser
		)
		if err := rows.Scan(&id, &u.Username, &u.DisplayName, &u.AvatarUrl, &u.HomeCity); err != nil {
			return nil, err
		}
		u.Id = id.String()
		if u.DisplayName == "" {
			u.DisplayName = u.Username
		}
		out[id] = &u
	}
	return out, rows.Err()
}

func (r *repository) UserIDByUsername(ctx context.Context, username string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM users WHERE username = $1 AND is_active`,
		strings.TrimPrefix(strings.TrimSpace(username), "@")).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (r *repository) MemberSince(ctx context.Context, id uuid.UUID) (time.Time, error) {
	var t time.Time
	err := r.db.QueryRow(ctx, `SELECT created_at FROM users WHERE id = $1`, id).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (r *repository) Relation(ctx context.Context, a, b uuid.UUID) (friends, blocked bool, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM friendships WHERE user_id = $1 AND friend_id = $2),
		       EXISTS (SELECT 1 FROM user_blocks
		               WHERE (blocker_id = $1 AND blocked_id = $2) OR (blocker_id = $2 AND blocked_id = $1))`,
		a, b).Scan(&friends, &blocked)
	return friends, blocked, err
}

func (r *repository) RelationOf(ctx context.Context, viewer, other uuid.UUID) (Relation, bool, error) {
	if viewer == other {
		return RelationSelf, false, nil
	}
	var blocks, blockedBy, friends, outgoing, incoming bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2),
		       EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = $2 AND blocked_id = $1),
		       EXISTS (SELECT 1 FROM friendships WHERE user_id = $1 AND friend_id = $2),
		       EXISTS (SELECT 1 FROM friend_requests WHERE from_user = $1 AND to_user = $2 AND status = 'pending'),
		       EXISTS (SELECT 1 FROM friend_requests WHERE from_user = $2 AND to_user = $1 AND status = 'pending')`,
		viewer, other).Scan(&blocks, &blockedBy, &friends, &outgoing, &incoming)
	if err != nil {
		return 0, false, err
	}
	switch {
	case blocks:
		return RelationBlocked, blockedBy, nil
	case friends:
		return RelationFriends, blockedBy, nil
	case outgoing:
		return RelationRequested, blockedBy, nil
	case incoming:
		return RelationIncoming, blockedBy, nil
	default:
		return RelationNone, blockedBy, nil
	}
}

func (r *repository) FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `SELECT friend_id FROM friendships WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (r *repository) ListFriends(ctx context.Context, userID uuid.UUID) ([]Friend, error) {
	rows, err := r.db.Query(ctx, `
		SELECT f.friend_id, f.created_at FROM friendships f
		JOIN users u ON u.id = f.friend_id AND u.is_active
		WHERE f.user_id = $1 ORDER BY f.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	type row struct {
		id    uuid.UUID
		since time.Time
	}
	var list []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.since); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(list))
	for i, x := range list {
		ids[i] = x.id
	}
	cards, err := r.PublicUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Friend, 0, len(list))
	for _, x := range list {
		if c := cards[x.id]; c != nil {
			out = append(out, Friend{User: c, Since: x.since})
		}
	}
	return out, nil
}

func (r *repository) RemoveFriend(ctx context.Context, a, b uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM friendships WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`, a, b)
	return err
}

// befriend inserts both directions and closes any pending request between
// the two, inside tx.
func befriend(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO friendships (user_id, friend_id) VALUES ($1, $2), ($2, $1)
		ON CONFLICT DO NOTHING`, a, b); err != nil {
		return fmt.Errorf("insert friendship: %w", err)
	}
	_, err := tx.Exec(ctx, `
		UPDATE friend_requests SET status = 'accepted', responded_at = NOW()
		WHERE status = 'pending'
		  AND ((from_user = $1 AND to_user = $2) OR (from_user = $2 AND to_user = $1))`, a, b)
	return err
}

func (r *repository) SendRequest(ctx context.Context, from, to uuid.UUID) (uuid.UUID, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// The other side already asked: the two requests meet.
	var reverse uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM friend_requests WHERE from_user = $1 AND to_user = $2 AND status = 'pending'
		FOR UPDATE`, to, from).Scan(&reverse)
	switch {
	case err == nil:
		if err := befriend(ctx, tx, from, to); err != nil {
			return uuid.Nil, false, err
		}
		return reverse, true, tx.Commit(ctx)
	case !errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, false, err
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO friend_requests (from_user, to_user) VALUES ($1, $2) RETURNING id`, from, to).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, false, ErrConflict
		}
		return uuid.Nil, false, fmt.Errorf("insert request: %w", err)
	}
	return id, false, tx.Commit(ctx)
}

func (r *repository) RespondRequest(ctx context.Context, id, to uuid.UUID, accept bool) (uuid.UUID, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var from uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT from_user FROM friend_requests
		WHERE id = $1 AND to_user = $2 AND status = 'pending' FOR UPDATE`, id, to).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if accept {
		if err := befriend(ctx, tx, from, to); err != nil {
			return uuid.Nil, err
		}
	} else if _, err := tx.Exec(ctx, `
		UPDATE friend_requests SET status = 'declined', responded_at = NOW() WHERE id = $1`, id); err != nil {
		return uuid.Nil, err
	}
	return from, tx.Commit(ctx)
}

func (r *repository) CancelRequest(ctx context.Context, id, from uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE friend_requests SET status = 'cancelled', responded_at = NOW()
		WHERE id = $1 AND from_user = $2 AND status = 'pending'`, id, from)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *repository) ListRequests(ctx context.Context, userID uuid.UUID, incoming bool) ([]Request, error) {
	col := "from_user"
	if incoming {
		col = "to_user"
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, from_user, to_user, created_at FROM friend_requests
		WHERE `+col+` = $1 AND status = 'pending' ORDER BY created_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, from, to uuid.UUID
		at           time.Time
	}
	var list []row
	ids := map[uuid.UUID]bool{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.from, &x.to, &x.at); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, x)
		ids[x.from], ids[x.to] = true, true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	keys := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	cards, err := r.PublicUsers(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make([]Request, 0, len(list))
	for _, x := range list {
		if cards[x.from] == nil || cards[x.to] == nil {
			continue // a deactivated account's request is not shown
		}
		out = append(out, Request{ID: x.id, From: cards[x.from], To: cards[x.to], CreatedAt: x.at})
	}
	return out, nil
}

func (r *repository) CountRequestsSince(ctx context.Context, from uuid.UUID, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM friend_requests WHERE from_user = $1 AND created_at >= $2`, from, since).Scan(&n)
	return n, err
}

func (r *repository) MakeFriends(ctx context.Context, a, b uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := befriend(ctx, tx, a, b); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *repository) Block(ctx context.Context, blocker, blocked uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		blocker, blocked); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM friendships WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`,
		blocker, blocked); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE friend_requests SET status = 'cancelled', responded_at = NOW()
		WHERE status = 'pending'
		  AND ((from_user = $1 AND to_user = $2) OR (from_user = $2 AND to_user = $1))`,
		blocker, blocked); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *repository) Unblock(ctx context.Context, blocker, blocked uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`, blocker, blocked)
	return err
}

func (r *repository) InviteFor(ctx context.Context, userID uuid.UUID) (*Invite, error) {
	inv := Invite{UserID: userID}
	err := r.db.QueryRow(ctx, `SELECT code, expires_at FROM user_invites WHERE user_id = $1`, userID).
		Scan(&inv.Code, &inv.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (r *repository) SaveInvite(ctx context.Context, inv Invite) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_invites (user_id, code, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET code = EXCLUDED.code, expires_at = EXCLUDED.expires_at,
		                                    created_at = NOW()`,
		inv.UserID, inv.Code, inv.ExpiresAt)
	return err
}

func (r *repository) InviteByCode(ctx context.Context, code string) (*Invite, error) {
	inv := Invite{Code: code}
	err := r.db.QueryRow(ctx, `
		SELECT i.user_id, i.expires_at FROM user_invites i
		JOIN users u ON u.id = i.user_id AND u.is_active
		WHERE i.code = $1`, code).Scan(&inv.UserID, &inv.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (r *repository) SetInvitedBy(ctx context.Context, invitee, inviter uuid.UUID) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE users SET invited_by_user_id = $2
		WHERE id = $1 AND invited_by_user_id IS NULL AND id <> $2`, invitee, inviter)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *repository) MatchHashes(ctx context.Context, hashes []string) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	if len(hashes) == 0 {
		return out, nil
	}
	// Two index lookups (idx_users_phone_sha256, idx_users_email_sha256),
	// each restricted to verified, active users exactly as the indexes are.
	rows, err := r.db.Query(ctx, `
		SELECT loci_sha256_hex(phone), id FROM users
		WHERE loci_sha256_hex(phone) = ANY($1)
		  AND phone IS NOT NULL AND phone_verified_at IS NOT NULL AND is_active
		UNION ALL
		SELECT loci_sha256_hex(lower(email::text)), id FROM users
		WHERE loci_sha256_hex(lower(email::text)) = ANY($1)
		  AND email IS NOT NULL AND email_verified_at IS NOT NULL AND is_active`, hashes)
	if err != nil {
		return nil, fmt.Errorf("match contacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			h  string
			id uuid.UUID
		)
		if err := rows.Scan(&h, &id); err != nil {
			return nil, err
		}
		out[h] = id
	}
	return out, rows.Err()
}

func (r *repository) SearchUsernames(ctx context.Context, prefix string, limit int) ([]uuid.UUID, error) {
	// Escape LIKE wildcards so the prefix is taken literally.
	p := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(prefix))
	rows, err := r.db.Query(ctx, `
		SELECT id FROM users
		WHERE username IS NOT NULL AND is_active AND lower(username::text) LIKE $1 || '%'
		ORDER BY length(username::text), username LIMIT $2`, p, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (r *repository) Stats(ctx context.Context, owner uuid.UUID, tripVisibilities []int32) (Stats, error) {
	var s Stats
	err := r.db.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(DISTINCT lower(city_name)) FROM user_visited_cities WHERE user_id = $1),
		  (SELECT COUNT(DISTINCT NULLIF(country, '')) FROM user_visited_cities WHERE user_id = $1),
		  (SELECT COUNT(*) FROM trips WHERE user_id = $1 AND visibility = ANY($2)),
		  (SELECT COUNT(*) FROM friendships WHERE user_id = $1)`,
		owner, tripVisibilities).Scan(&s.Cities, &s.Countries, &s.VisibleTrips, &s.Friends)
	return s, err
}

func (r *repository) FacebookFriends(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, bool, error) {
	var linked bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_providers WHERE user_id = $1 AND provider = 'facebook')`, userID).
		Scan(&linked); err != nil {
		return nil, false, fmt.Errorf("check facebook link: %w", err)
	}
	if !linked {
		return nil, false, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT p.user_id
		FROM user_facebook_friends f
		JOIN user_providers p ON p.provider = 'facebook' AND p.provider_user_id = f.facebook_id
		JOIN users u ON u.id = p.user_id AND u.is_active
		WHERE f.user_id = $1 AND p.user_id <> $1`, userID)
	if err != nil {
		return nil, true, fmt.Errorf("match facebook friends: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, true, fmt.Errorf("scan facebook friend: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, true, rows.Err()
}
