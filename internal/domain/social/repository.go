// Package social is Loci's people layer: following, follow requests to
// private accounts, invite links, blocks, contact matching and public
// profiles.
//
// Following is one-way. Two people who follow each other are friends, and
// "Friends" trip visibility is seen only by friends: both sides chose it. The
// older friend RPCs are served from the same graph (see Edge.Legacy).
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
	// ErrPrivate is content of a private account the caller is not an
	// approved follower of.
	ErrPrivate = errors.New("this account is private")
)

// Relation is the caller's relation to another user in the older friend
// model (Relationship in social.proto).
type Relation int32

const (
	RelationNone      Relation = 1
	RelationRequested Relation = 2
	RelationIncoming  Relation = 3
	RelationFriends   Relation = 4
	RelationBlocked   Relation = 5
	RelationSelf      Relation = 6
)

// FollowState is whether the caller follows another user (FollowState in
// social.proto).
type FollowState int32

const (
	FollowNone      FollowState = 1
	FollowRequested FollowState = 2
	FollowFollowing FollowState = 3
	FollowBlocked   FollowState = 4
	FollowSelf      FollowState = 5
)

// Edge is how a viewer stands with another user.
type Edge struct {
	// State is the viewer's side: whether they follow the other user.
	State FollowState
	// FollowsYou: the other user follows the viewer.
	FollowsYou bool
	// Incoming: the other user asked to follow the viewer, still pending.
	Incoming bool
	// BlockedBy: the other user blocked the viewer. Callers turn it into
	// NotFound.
	BlockedBy bool
	// Private: the other user's account is private.
	Private bool
}

// Friends is a mutual follow.
func (e Edge) Friends() bool { return e.State == FollowFollowing && e.FollowsYou }

// CanView reports whether the viewer may see the other user's trips,
// achievements and follow lists: their own, a public account's, or a private
// account they are an approved follower of.
func (e Edge) CanView() bool {
	return e.State == FollowSelf || (e.State != FollowBlocked && (!e.Private || e.State == FollowFollowing))
}

// Legacy is the edge in the older friend model, for clients that still read
// Relationship: friends when each follows the other, requested when only the
// viewer follows (or asked to), incoming when only the other user does.
func (e Edge) Legacy() Relation {
	switch {
	case e.State == FollowSelf:
		return RelationSelf
	case e.State == FollowBlocked:
		return RelationBlocked
	case e.Friends():
		return RelationFriends
	case e.State == FollowFollowing || e.State == FollowRequested:
		return RelationRequested
	case e.FollowsYou || e.Incoming:
		return RelationIncoming
	default:
		return RelationNone
	}
}

// Friend is one friendship (a mutual follow) as seen by one side.
type Friend struct {
	User  *socialv1.PublicUser
	Since time.Time
}

// Follow is one person in a followers or following list.
type Follow struct {
	User  *socialv1.PublicUser
	Since time.Time
}

// Request is a follow request (or, for older clients, a friend request) with
// both sides' cards.
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
	Followers, Following                     int32
}

// Repository is the social store.
type Repository interface {
	PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error)
	UserIDByUsername(ctx context.Context, username string) (uuid.UUID, error)
	MemberSince(ctx context.Context, id uuid.UUID) (time.Time, error)

	// Relation reports a mutual follow and a block in either direction.
	Relation(ctx context.Context, a, b uuid.UUID) (friends, blocked bool, err error)
	// Edges is viewer's edge to each of others (viewer itself included).
	Edges(ctx context.Context, viewer uuid.UUID, others []uuid.UUID) (map[uuid.UUID]Edge, error)
	// FriendIDs lists a user's mutual follows; FollowingIDs whom they follow;
	// FollowerIDs who follows them.
	FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	FollowingIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	FollowerIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	ListFriends(ctx context.Context, userID uuid.UUID) ([]Friend, error)
	// ListFollows lists userID's followers (or the people they follow),
	// newest first, with the total.
	ListFollows(ctx context.Context, userID uuid.UUID, followers bool, limit, offset int) ([]Follow, int, error)

	// Follow makes from follow to, or, when to is private, records a pending
	// request. It returns the resulting state and the id of the follow or the
	// request. Following again changes nothing.
	Follow(ctx context.Context, from, to uuid.UUID) (FollowState, uuid.UUID, error)
	// Unfollow ends from's follow of to and withdraws any pending request.
	Unfollow(ctx context.Context, from, to uuid.UUID) error
	// FollowByID resolves a follow by its id.
	FollowByID(ctx context.Context, id uuid.UUID) (follower, followee uuid.UUID, err error)
	// RespondRequest answers a pending request addressed to `to`; it returns
	// the sender. Approving makes the sender a follower.
	RespondRequest(ctx context.Context, id, to uuid.UUID, approve bool) (from uuid.UUID, err error)
	CancelRequest(ctx context.Context, id, from uuid.UUID) error
	ListRequests(ctx context.Context, userID uuid.UUID, incoming bool) ([]Request, error)
	// ListOneWay lists follows that are not returned, as requests: incoming
	// is people following userID whom userID does not follow back, outgoing
	// the reverse. Request.ID is the follow's id.
	ListOneWay(ctx context.Context, userID uuid.UUID, incoming bool, limit int) ([]Request, error)
	// CountFollowsSince counts follows and follow requests from has started
	// since then.
	CountFollowsSince(ctx context.Context, from uuid.UUID, since time.Time) (int, error)

	// MakeFriends makes a and b follow each other at once (an accepted
	// invite), whatever their privacy, closing any pending request between
	// them.
	MakeFriends(ctx context.Context, a, b uuid.UUID) error

	// Privacy reads whether userID is private and how many requests wait.
	Privacy(ctx context.Context, userID uuid.UUID) (private bool, pending int32, err error)
	// SetPrivate sets userID's privacy. Going public approves every pending
	// request; it returns the senders it approved.
	SetPrivate(ctx context.Context, userID uuid.UUID, private bool) (approved []uuid.UUID, err error)

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

	// ClaimPushSlot reserves today's (UTC) push of kind for userID; false
	// when one was already sent.
	ClaimPushSlot(ctx context.Context, userID uuid.UUID, kind string) (bool, error)
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
		SELECT EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2)
		       AND EXISTS (SELECT 1 FROM follows WHERE follower_id = $2 AND followee_id = $1),
		       EXISTS (SELECT 1 FROM user_blocks
		               WHERE (blocker_id = $1 AND blocked_id = $2) OR (blocker_id = $2 AND blocked_id = $1))`,
		a, b).Scan(&friends, &blocked)
	return friends, blocked, err
}

func (r *repository) Edges(ctx context.Context, viewer uuid.UUID, others []uuid.UUID) (map[uuid.UUID]Edge, error) {
	out := make(map[uuid.UUID]Edge, len(others))
	if len(others) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT o.id, u.is_private,
		       EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = $1 AND blocked_id = o.id),
		       EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = o.id AND blocked_id = $1),
		       EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = o.id),
		       EXISTS (SELECT 1 FROM follows WHERE follower_id = o.id AND followee_id = $1),
		       EXISTS (SELECT 1 FROM follow_requests
		               WHERE from_user = $1 AND to_user = o.id AND status = 'pending'),
		       EXISTS (SELECT 1 FROM follow_requests
		               WHERE from_user = o.id AND to_user = $1 AND status = 'pending')
		FROM unnest($2::uuid[]) AS o(id)
		JOIN users u ON u.id = o.id`, viewer, others)
	if err != nil {
		return nil, fmt.Errorf("edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                           uuid.UUID
			e                            Edge
			blocks, following, requested bool
		)
		if err := rows.Scan(&id, &e.Private, &blocks, &e.BlockedBy, &following, &e.FollowsYou,
			&requested, &e.Incoming); err != nil {
			return nil, err
		}
		switch {
		case id == viewer:
			e = Edge{State: FollowSelf, Private: e.Private}
		case blocks:
			e.State = FollowBlocked
		case following:
			e.State = FollowFollowing
		case requested:
			e.State = FollowRequested
		default:
			e.State = FollowNone
		}
		out[id] = e
	}
	return out, rows.Err()
}

func (r *repository) FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.followee_id FROM follows a
		JOIN follows b ON b.follower_id = a.followee_id AND b.followee_id = a.follower_id
		WHERE a.follower_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (r *repository) FollowingIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `SELECT followee_id FROM follows WHERE follower_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (r *repository) FollowerIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `SELECT follower_id FROM follows WHERE followee_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// idSince is a user id and when the link to them began.
type idSince struct {
	id    uuid.UUID
	since time.Time
}

func collectIDSince(rows pgx.Rows) ([]idSince, error) {
	defer rows.Close()
	var list []idSince
	for rows.Next() {
		var x idSince
		if err := rows.Scan(&x.id, &x.since); err != nil {
			return nil, err
		}
		list = append(list, x)
	}
	return list, rows.Err()
}

func (r *repository) cardsFor(ctx context.Context, list []idSince) ([]Follow, error) {
	ids := make([]uuid.UUID, len(list))
	for i, x := range list {
		ids[i] = x.id
	}
	cards, err := r.PublicUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Follow, 0, len(list))
	for _, x := range list {
		if c := cards[x.id]; c != nil {
			out = append(out, Follow{User: c, Since: x.since})
		}
	}
	return out, nil
}

func (r *repository) ListFriends(ctx context.Context, userID uuid.UUID) ([]Friend, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.followee_id, GREATEST(a.created_at, b.created_at) AS since FROM follows a
		JOIN follows b ON b.follower_id = a.followee_id AND b.followee_id = a.follower_id
		WHERE a.follower_id = $1 ORDER BY since DESC`, userID)
	if err != nil {
		return nil, err
	}
	list, err := collectIDSince(rows)
	if err != nil {
		return nil, err
	}
	follows, err := r.cardsFor(ctx, list)
	if err != nil {
		return nil, err
	}
	out := make([]Friend, len(follows))
	for i, f := range follows {
		out[i] = Friend(f)
	}
	return out, nil
}

func (r *repository) ListFollows(ctx context.Context, userID uuid.UUID, followers bool, limit, offset int) ([]Follow, int, error) {
	// followers: rows where userID is followed; the listed person is the
	// follower. Otherwise the listed person is whom userID follows.
	mine, other := "follower_id", "followee_id"
	if followers {
		mine, other = "followee_id", "follower_id"
	}
	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM follows f JOIN users u ON u.id = f.`+other+` AND u.is_active
		WHERE f.`+mine+` = $1`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count follows: %w", err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT f.`+other+`, f.created_at FROM follows f
		JOIN users u ON u.id = f.`+other+` AND u.is_active
		WHERE f.`+mine+` = $1
		ORDER BY f.created_at DESC, f.`+other+`
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list follows: %w", err)
	}
	list, err := collectIDSince(rows)
	if err != nil {
		return nil, 0, err
	}
	out, err := r.cardsFor(ctx, list)
	return out, total, err
}

func (r *repository) Follow(ctx context.Context, from, to uuid.UUID) (FollowState, uuid.UUID, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Lock the followee's row so a privacy switch cannot race the decision
	// between following and asking.
	var private bool
	err = tx.QueryRow(ctx, `SELECT is_private FROM users WHERE id = $1 AND is_active FOR SHARE`, to).Scan(&private)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return 0, uuid.Nil, err
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM follows WHERE follower_id = $1 AND followee_id = $2`, from, to).Scan(&id)
	switch {
	case err == nil:
		return FollowFollowing, id, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return 0, uuid.Nil, err
	}

	if !private {
		if err := tx.QueryRow(ctx, `
			INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2) RETURNING id`, from, to).Scan(&id); err != nil {
			return 0, uuid.Nil, fmt.Errorf("insert follow: %w", err)
		}
		return FollowFollowing, id, tx.Commit(ctx)
	}

	err = tx.QueryRow(ctx, `
		SELECT id FROM follow_requests WHERE from_user = $1 AND to_user = $2 AND status = 'pending'`,
		from, to).Scan(&id)
	switch {
	case err == nil:
		return FollowRequested, id, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return 0, uuid.Nil, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO follow_requests (from_user, to_user) VALUES ($1, $2) RETURNING id`, from, to).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return FollowRequested, uuid.Nil, nil
		}
		return 0, uuid.Nil, fmt.Errorf("insert follow request: %w", err)
	}
	return FollowRequested, id, tx.Commit(ctx)
}

func (r *repository) Unfollow(ctx context.Context, from, to uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, from, to); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE follow_requests SET status = 'cancelled', responded_at = NOW()
		WHERE from_user = $1 AND to_user = $2 AND status = 'pending'`, from, to); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *repository) FollowByID(ctx context.Context, id uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var follower, followee uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT follower_id, followee_id FROM follows WHERE id = $1`, id).
		Scan(&follower, &followee)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	return follower, followee, err
}

func (r *repository) RespondRequest(ctx context.Context, id, to uuid.UUID, approve bool) (uuid.UUID, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var from uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT from_user FROM follow_requests
		WHERE id = $1 AND to_user = $2 AND status = 'pending' FOR UPDATE`, id, to).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	status := "declined"
	if approve {
		status = "approved"
		if _, err := tx.Exec(ctx, `
			INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			from, to); err != nil {
			return uuid.Nil, fmt.Errorf("insert follow: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE follow_requests SET status = $2, responded_at = NOW() WHERE id = $1`, id, status); err != nil {
		return uuid.Nil, err
	}
	return from, tx.Commit(ctx)
}

func (r *repository) CancelRequest(ctx context.Context, id, from uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE follow_requests SET status = 'cancelled', responded_at = NOW()
		WHERE id = $1 AND from_user = $2 AND status = 'pending'`, id, from)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// requestRow is a request before the cards are loaded.
type requestRow struct {
	id, from, to uuid.UUID
	at           time.Time
}

func (r *repository) requestsWithCards(ctx context.Context, rows pgx.Rows) ([]Request, error) {
	defer rows.Close()
	var list []requestRow
	ids := map[uuid.UUID]bool{}
	for rows.Next() {
		var x requestRow
		if err := rows.Scan(&x.id, &x.from, &x.to, &x.at); err != nil {
			return nil, err
		}
		list = append(list, x)
		ids[x.from], ids[x.to] = true, true
	}
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

func (r *repository) ListRequests(ctx context.Context, userID uuid.UUID, incoming bool) ([]Request, error) {
	col := "from_user"
	if incoming {
		col = "to_user"
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, from_user, to_user, created_at FROM follow_requests
		WHERE `+col+` = $1 AND status = 'pending' ORDER BY created_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	return r.requestsWithCards(ctx, rows)
}

func (r *repository) ListOneWay(ctx context.Context, userID uuid.UUID, incoming bool, limit int) ([]Request, error) {
	col := "follower_id"
	if incoming {
		col = "followee_id"
	}
	rows, err := r.db.Query(ctx, `
		SELECT f.id, f.follower_id, f.followee_id, f.created_at FROM follows f
		WHERE f.`+col+` = $1
		  AND NOT EXISTS (SELECT 1 FROM follows b
		                  WHERE b.follower_id = f.followee_id AND b.followee_id = f.follower_id)
		ORDER BY f.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return r.requestsWithCards(ctx, rows)
}

func (r *repository) CountFollowsSince(ctx context.Context, from uuid.UUID, since time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM follows WHERE follower_id = $1 AND created_at >= $2)
		     + (SELECT COUNT(*) FROM follow_requests WHERE from_user = $1 AND created_at >= $2)`,
		from, since).Scan(&n)
	return n, err
}

func (r *repository) MakeFriends(ctx context.Context, a, b uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `
		INSERT INTO follows (follower_id, followee_id) VALUES ($1, $2), ($2, $1)
		ON CONFLICT DO NOTHING`, a, b); err != nil {
		return fmt.Errorf("insert follows: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE follow_requests SET status = 'approved', responded_at = NOW()
		WHERE status = 'pending'
		  AND ((from_user = $1 AND to_user = $2) OR (from_user = $2 AND to_user = $1))`, a, b); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *repository) Privacy(ctx context.Context, userID uuid.UUID) (bool, int32, error) {
	var (
		private bool
		pending int32
	)
	err := r.db.QueryRow(ctx, `
		SELECT is_private,
		       (SELECT COUNT(*) FROM follow_requests WHERE to_user = $1 AND status = 'pending')
		FROM users WHERE id = $1`, userID).Scan(&private, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, ErrNotFound
	}
	return private, pending, err
}

func (r *repository) SetPrivate(ctx context.Context, userID uuid.UUID, private bool) ([]uuid.UUID, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `UPDATE users SET is_private = $2 WHERE id = $1`, userID, private); err != nil {
		return nil, fmt.Errorf("set privacy: %w", err)
	}
	var approved []uuid.UUID
	if !private {
		rows, err := tx.Query(ctx, `
			UPDATE follow_requests SET status = 'approved', responded_at = NOW()
			WHERE to_user = $1 AND status = 'pending' RETURNING from_user`, userID)
		if err != nil {
			return nil, err
		}
		if approved, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO follows (follower_id, followee_id)
			SELECT unnest($2::uuid[]), $1 ON CONFLICT DO NOTHING`, userID, approved); err != nil {
			return nil, fmt.Errorf("approve pending: %w", err)
		}
	}
	return approved, tx.Commit(ctx)
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
		DELETE FROM follows
		WHERE (follower_id = $1 AND followee_id = $2) OR (follower_id = $2 AND followee_id = $1)`,
		blocker, blocked); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE follow_requests SET status = 'cancelled', responded_at = NOW()
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
		  (SELECT COUNT(*) FROM follows a
		   JOIN follows b ON b.follower_id = a.followee_id AND b.followee_id = a.follower_id
		   WHERE a.follower_id = $1),
		  (SELECT COUNT(*) FROM follows f JOIN users u ON u.id = f.follower_id AND u.is_active
		   WHERE f.followee_id = $1),
		  (SELECT COUNT(*) FROM follows f JOIN users u ON u.id = f.followee_id AND u.is_active
		   WHERE f.follower_id = $1)`,
		owner, tripVisibilities).Scan(&s.Cities, &s.Countries, &s.VisibleTrips, &s.Friends,
		&s.Followers, &s.Following)
	return s, err
}

func (r *repository) ClaimPushSlot(ctx context.Context, userID uuid.UUID, kind string) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		INSERT INTO progress_push_log (user_id, kind, local_date) VALUES ($1, $2, CURRENT_DATE)
		ON CONFLICT DO NOTHING`, userID, kind)
	if err != nil {
		return false, fmt.Errorf("claim push slot: %w", err)
	}
	return ct.RowsAffected() == 1, nil
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
