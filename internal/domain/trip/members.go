package trip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Planning together: the owner invites people by link and they join the trip
// as members. The owner is the trip's user_id and never a member row.

var (
	// ErrInviteDead: the invite expired, was revoked or ran out of uses.
	ErrInviteDead = errors.New("this invite no longer works")
	// ErrTooManyJoins: the caller joined too many trips in the last hour.
	ErrTooManyJoins = errors.New("too many trips joined in the last hour")
)

const (
	// inviteTTL is how long a fresh invite link works.
	inviteTTL = 14 * 24 * time.Hour
	// inviteMaxUses is how many people one link lets in.
	inviteMaxUses = 10
	// maxJoinsPerHour bounds AcceptTripInvite per caller.
	maxJoinsPerHour = 20
)

// InviteURL is where a trip invite opens: the web app's /j/{code}, which
// the iOS app also claims as a Universal Link. /t/{code} stays the read-only
// share link.
func InviteURL(code string) string { return ShareOrigin + "/j/" + code }

// Member is someone on a trip who is not its owner.
type Member struct {
	UserID   uuid.UUID
	Role     Role
	JoinedAt time.Time
}

// Invite is a link that adds whoever opens it to a trip.
type Invite struct {
	Code      string
	TripID    uuid.UUID
	Role      Role
	CreatedBy uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
	MaxUses   int
	Uses      int
	RevokedAt *time.Time
}

// Live reports whether the invite still lets people in at now.
func (i *Invite) Live(now time.Time) bool {
	return i.RevokedAt == nil && now.Before(i.ExpiresAt) && i.Uses < i.MaxUses
}

// Joined is the outcome of accepting an invite.
type Joined struct {
	Role Role
	// New is false when the caller was already on the trip; their role is
	// then the one they had.
	New bool
	// NewUser is true when the caller's account is younger than the invite:
	// the invite brought them to Loci.
	NewUser bool
}

func roleColumn(r Role) string {
	if r == RoleViewer {
		return "viewer"
	}
	return "co_planner"
}

func roleFromColumn(s string) Role {
	switch s {
	case "co_planner":
		return RoleCoPlanner
	case "viewer":
		return RoleViewer
	default:
		return RoleNone
	}
}

// MembersRepository stores trip members and invites.
type MembersRepository interface {
	// Role is userID's role on tripID as a member; RoleNone when they are not
	// one. It does not know about the owner.
	Role(ctx context.Context, tripID, userID uuid.UUID) (Role, error)
	// Members lists a trip's members by joining order.
	Members(ctx context.Context, tripID uuid.UUID) ([]Member, error)
	// RemoveMember takes userID off the trip. Removing a non-member succeeds.
	RemoveMember(ctx context.Context, tripID, userID uuid.UUID) error
	// LiveInvite is the trip's newest invite for role that still works at
	// now, or nil.
	LiveInvite(ctx context.Context, tripID uuid.UUID, role Role, now time.Time) (*Invite, error)
	CreateInvite(ctx context.Context, inv *Invite) error
	// InviteByCode loads any invite, live or not. ErrNotFound when unknown.
	InviteByCode(ctx context.Context, code string) (*Invite, error)
	// RevokeInvite stops a trip's invite. ErrNotFound when the trip has no
	// such invite.
	RevokeInvite(ctx context.Context, tripID uuid.UUID, code string, now time.Time) error
	// Accept adds userID to the invite's trip in one transaction: the invite
	// is locked, checked live (ErrInviteDead), and its use counted only when
	// the caller was not already on the trip.
	Accept(ctx context.Context, code string, userID uuid.UUID, now time.Time) (*Joined, error)
	// JoinsSince counts the invites userID accepted after since.
	JoinsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
	// ListWithMember lists userID's own trips and the trips they are a
	// member of, most recently updated first, with their member role for the
	// latter.
	ListWithMember(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Trip, map[uuid.UUID]Role, int, error)
}

type membersRepository struct {
	db    *pgxpool.Pool
	trips *sharingRepository
}

// NewMembersRepository is the Postgres MembersRepository.
func NewMembersRepository(db *pgxpool.Pool, logger *slog.Logger) MembersRepository {
	return &membersRepository{db: db, trips: &sharingRepository{db: db, days: &repository{db: db, logger: logger}}}
}

func (r *membersRepository) Role(ctx context.Context, tripID, userID uuid.UUID) (Role, error) {
	var role string
	err := r.db.QueryRow(ctx, `SELECT role FROM trip_members WHERE trip_id = $1 AND user_id = $2`, tripID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleNone, nil
	}
	if err != nil {
		return RoleNone, fmt.Errorf("trip member role: %w", err)
	}
	return roleFromColumn(role), nil
}

func (r *membersRepository) Members(ctx context.Context, tripID uuid.UUID) ([]Member, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id, role, joined_at FROM trip_members
		WHERE trip_id = $1 ORDER BY joined_at, user_id`, tripID)
	if err != nil {
		return nil, fmt.Errorf("list trip members: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var (
			m    Member
			role string
		)
		if err := rows.Scan(&m.UserID, &role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan trip member: %w", err)
		}
		m.Role = roleFromColumn(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *membersRepository) RemoveMember(ctx context.Context, tripID, userID uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM trip_members WHERE trip_id = $1 AND user_id = $2`, tripID, userID); err != nil {
		return fmt.Errorf("remove trip member: %w", err)
	}
	return nil
}

const inviteColumns = `code, trip_id, role, created_by, created_at, expires_at, max_uses, uses, revoked_at`

func scanInvite(row pgx.Row) (*Invite, error) {
	var (
		inv  Invite
		role string
	)
	if err := row.Scan(&inv.Code, &inv.TripID, &role, &inv.CreatedBy, &inv.CreatedAt, &inv.ExpiresAt, &inv.MaxUses, &inv.Uses, &inv.RevokedAt); err != nil {
		return nil, err
	}
	inv.Role = roleFromColumn(role)
	return &inv, nil
}

func (r *membersRepository) LiveInvite(ctx context.Context, tripID uuid.UUID, role Role, now time.Time) (*Invite, error) {
	inv, err := scanInvite(r.db.QueryRow(ctx, `
		SELECT `+inviteColumns+` FROM trip_invites
		WHERE trip_id = $1 AND role = $2 AND revoked_at IS NULL AND expires_at > $3 AND uses < max_uses
		ORDER BY created_at DESC LIMIT 1`, tripID, roleColumn(role), now))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("live trip invite: %w", err)
	}
	return inv, nil
}

func (r *membersRepository) CreateInvite(ctx context.Context, inv *Invite) error {
	if err := r.db.QueryRow(ctx, `
		INSERT INTO trip_invites (code, trip_id, role, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		inv.Code, inv.TripID, roleColumn(inv.Role), inv.CreatedBy, inv.ExpiresAt, inv.MaxUses).Scan(&inv.CreatedAt); err != nil {
		return fmt.Errorf("create trip invite: %w", err)
	}
	return nil
}

func (r *membersRepository) InviteByCode(ctx context.Context, code string) (*Invite, error) {
	inv, err := scanInvite(r.db.QueryRow(ctx, `SELECT `+inviteColumns+` FROM trip_invites WHERE code = $1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("trip invite: %w", err)
	}
	return inv, nil
}

func (r *membersRepository) RevokeInvite(ctx context.Context, tripID uuid.UUID, code string, now time.Time) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE trip_invites SET revoked_at = COALESCE(revoked_at, $3)
		WHERE code = $1 AND trip_id = $2`, code, tripID, now)
	if err != nil {
		return fmt.Errorf("revoke trip invite: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *membersRepository) Accept(ctx context.Context, code string, userID uuid.UUID, now time.Time) (*Joined, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin accept: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inv, err := scanInvite(tx.QueryRow(ctx, `SELECT `+inviteColumns+` FROM trip_invites WHERE code = $1 FOR UPDATE`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock trip invite: %w", err)
	}

	// Already on the trip: keep the role they have, use nothing up. Checked
	// before liveness so a member re-opening an old link is not told it died.
	var existing string
	err = tx.QueryRow(ctx, `SELECT role FROM trip_members WHERE trip_id = $1 AND user_id = $2`, inv.TripID, userID).Scan(&existing)
	if err == nil {
		return &Joined{Role: roleFromColumn(existing)}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check trip member: %w", err)
	}
	if !inv.Live(now) {
		return nil, ErrInviteDead
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO trip_members (trip_id, user_id, role, invited_by) VALUES ($1, $2, $3, $4)`,
		inv.TripID, userID, roleColumn(inv.Role), inv.CreatedBy); err != nil {
		return nil, fmt.Errorf("add trip member: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE trip_invites SET uses = uses + 1 WHERE code = $1`, code); err != nil {
		return nil, fmt.Errorf("count invite use: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trip_invite_accepts (code, user_id, accepted_at) VALUES ($1, $2, $3)
		ON CONFLICT (code, user_id) DO UPDATE SET accepted_at = EXCLUDED.accepted_at`, code, userID, now); err != nil {
		return nil, fmt.Errorf("record invite accept: %w", err)
	}
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM users WHERE id = $1`, userID).Scan(&created); err != nil {
		return nil, fmt.Errorf("load joining user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit accept: %w", err)
	}
	return &Joined{Role: inv.Role, New: true, NewUser: created.After(inv.CreatedAt)}, nil
}

func (r *membersRepository) JoinsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	var n int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM trip_invite_accepts WHERE user_id = $1 AND accepted_at > $2`, userID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("count trip joins: %w", err)
	}
	return n, nil
}

func (r *membersRepository) ListWithMember(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Trip, map[uuid.UUID]Role, int, error) {
	trips, total, err := r.trips.listTrips(ctx,
		`user_id = $1 OR id IN (SELECT trip_id FROM trip_members WHERE user_id = $1)`,
		[]any{userID}, limit, offset)
	if err != nil {
		return nil, nil, 0, err
	}
	var shared []uuid.UUID
	for _, t := range trips {
		if t.UserID != userID {
			shared = append(shared, t.ID)
		}
	}
	roles := map[uuid.UUID]Role{}
	if len(shared) == 0 {
		return trips, roles, total, nil
	}
	rows, err := r.db.Query(ctx, `SELECT trip_id, role FROM trip_members WHERE user_id = $1 AND trip_id = ANY($2)`, userID, shared)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("member roles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id   uuid.UUID
			role string
		)
		if err := rows.Scan(&id, &role); err != nil {
			return nil, nil, 0, fmt.Errorf("scan member role: %w", err)
		}
		roles[id] = roleFromColumn(role)
	}
	return trips, roles, total, rows.Err()
}
