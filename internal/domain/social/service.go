package social

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

// InviteOrigin is where an invite link opens: the web app's /invite/{code},
// which the iOS app also claims as a Universal Link.
const InviteOrigin = "https://lociai.fyi"

// Limits on actions a person could use to spam or to probe who is on Loci.
const (
	inviteLifetime        = 30 * 24 * time.Hour
	maxRequestsPerDay     = 20
	maxContactMatchPerDay = 5
	maxSearchesPerHour    = 100
)

var (
	// ErrSelf is an action aimed at the caller themselves.
	ErrSelf = errors.New("that is you")
	// ErrLimited is a per-action limit reached.
	ErrLimited = errors.New("limit reached, try again later")
)

// Notifier announces social events on the recipient's devices. Delivery is
// best effort and never fails the action.
type Notifier interface {
	FriendRequest(ctx context.Context, to uuid.UUID, from *socialv1.PublicUser)
	FriendAccepted(ctx context.Context, to uuid.UUID, by *socialv1.PublicUser)
}

type noopNotifier struct{}

func (noopNotifier) FriendRequest(context.Context, uuid.UUID, *socialv1.PublicUser)  {}
func (noopNotifier) FriendAccepted(context.Context, uuid.UUID, *socialv1.PublicUser) {}

// Service is the friends layer's logic.
type Service struct {
	repo     Repository
	notify   Notifier
	log      *slog.Logger
	now      func() time.Time
	contacts *windowLimiter
	searches *windowLimiter
}

// NewService builds the service. A nil notifier sends nothing.
func NewService(repo Repository, notify Notifier, log *slog.Logger) *Service {
	if notify == nil {
		notify = noopNotifier{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:     repo,
		notify:   notify,
		log:      log,
		now:      time.Now,
		contacts: newWindowLimiter(maxContactMatchPerDay, 24*time.Hour),
		searches: newWindowLimiter(maxSearchesPerHour, time.Hour),
	}
}

// Graph is the service as trip sharing's SocialGraph.
func (s *Service) Relation(ctx context.Context, a, b uuid.UUID) (bool, bool, error) {
	return s.repo.Relation(ctx, a, b)
}

func (s *Service) FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.repo.FriendIDs(ctx, userID)
}

func (s *Service) PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	return s.repo.PublicUsers(ctx, ids)
}

func newCode() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// InviteURL is the link an invite code opens.
func InviteURL(code string) string { return InviteOrigin + "/invite/" + code }

// MyInvite returns the caller's live invite, minting or renewing it.
func (s *Service) MyInvite(ctx context.Context, userID uuid.UUID) (*Invite, error) {
	inv, err := s.repo.InviteFor(ctx, userID)
	if err == nil && inv.ExpiresAt.After(s.now()) {
		return inv, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.RotateInvite(ctx, userID)
}

// RotateInvite replaces the caller's code; the old link stops working.
func (s *Service) RotateInvite(ctx context.Context, userID uuid.UUID) (*Invite, error) {
	inv := Invite{UserID: userID, Code: newCode(), ExpiresAt: s.now().Add(inviteLifetime)}
	if err := s.repo.SaveInvite(ctx, inv); err != nil {
		return nil, fmt.Errorf("save invite: %w", err)
	}
	return &inv, nil
}

// LookupInvite resolves a live code.
func (s *Service) LookupInvite(ctx context.Context, code string) (*Invite, error) {
	inv, err := s.repo.InviteByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if !inv.ExpiresAt.After(s.now()) {
		return nil, ErrNotFound
	}
	return inv, nil
}

// AcceptInvite makes the caller and the inviter friends. A blocked pair is
// NotFound, as if the invite did not exist.
func (s *Service) AcceptInvite(ctx context.Context, userID uuid.UUID, code string) (*socialv1.PublicUser, time.Time, error) {
	inv, err := s.LookupInvite(ctx, code)
	if err != nil {
		return nil, time.Time{}, err
	}
	if inv.UserID == userID {
		return nil, time.Time{}, ErrSelf
	}
	_, blocked, err := s.repo.Relation(ctx, userID, inv.UserID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if blocked {
		return nil, time.Time{}, ErrNotFound
	}
	if err := s.repo.MakeFriends(ctx, userID, inv.UserID); err != nil {
		return nil, time.Time{}, err
	}
	cards, err := s.repo.PublicUsers(ctx, []uuid.UUID{userID, inv.UserID})
	if err != nil {
		return nil, time.Time{}, err
	}
	s.notify.FriendAccepted(ctx, inv.UserID, cards[userID])
	return cards[inv.UserID], s.now(), nil
}

// SendRequest asks target to be friends. When target had already asked the
// caller, the two become friends at once.
func (s *Service) SendRequest(ctx context.Context, from, to uuid.UUID) (Relation, uuid.UUID, error) {
	if from == to {
		return 0, uuid.Nil, ErrSelf
	}
	rel, blockedBy, err := s.repo.RelationOf(ctx, from, to)
	if err != nil {
		return 0, uuid.Nil, err
	}
	switch {
	case blockedBy || rel == RelationBlocked:
		return 0, uuid.Nil, ErrNotFound
	case rel == RelationFriends || rel == RelationRequested:
		return rel, uuid.Nil, nil
	}
	n, err := s.repo.CountRequestsSince(ctx, from, s.now().Add(-24*time.Hour))
	if err != nil {
		return 0, uuid.Nil, err
	}
	if n >= maxRequestsPerDay {
		return 0, uuid.Nil, ErrLimited
	}
	id, friends, err := s.repo.SendRequest(ctx, from, to)
	if errors.Is(err, ErrConflict) {
		return RelationRequested, uuid.Nil, nil
	}
	if err != nil {
		return 0, uuid.Nil, err
	}
	cards, err := s.repo.PublicUsers(ctx, []uuid.UUID{from})
	if err != nil {
		s.log.Warn("load sender card", slog.Any("error", err))
	}
	if friends {
		s.notify.FriendAccepted(ctx, to, cards[from])
		return RelationFriends, id, nil
	}
	s.notify.FriendRequest(ctx, to, cards[from])
	return RelationRequested, id, nil
}

// Respond answers a request addressed to the caller.
func (s *Service) Respond(ctx context.Context, userID, requestID uuid.UUID, accept bool) (*socialv1.PublicUser, error) {
	from, err := s.repo.RespondRequest(ctx, requestID, userID, accept)
	if err != nil {
		return nil, err
	}
	if !accept {
		return nil, nil
	}
	cards, err := s.repo.PublicUsers(ctx, []uuid.UUID{from, userID})
	if err != nil {
		return nil, err
	}
	s.notify.FriendAccepted(ctx, from, cards[userID])
	return cards[from], nil
}

// Block ends any friendship and pending request between the two.
func (s *Service) Block(ctx context.Context, blocker, blocked uuid.UUID) error {
	if blocker == blocked {
		return ErrSelf
	}
	return s.repo.Block(ctx, blocker, blocked)
}

// Contact is one matched contact.
type Contact struct {
	Hash     string
	User     *socialv1.PublicUser
	Relation Relation
}

// MatchContacts finds Loci users among hashed contacts. The caller, and
// users who blocked the caller or whom the caller blocked, are left out.
func (s *Service) MatchContacts(ctx context.Context, userID uuid.UUID, hashes []string) ([]Contact, error) {
	if !s.contacts.Allow(userID, s.now()) {
		return nil, ErrLimited
	}
	found, err := s.repo.MatchHashes(ctx, hashes)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(found))
	for _, id := range found {
		if id != userID {
			ids = append(ids, id)
		}
	}
	cards, err := s.repo.PublicUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(found))
	for _, h := range hashes {
		id, ok := found[h]
		if !ok || id == userID || cards[id] == nil {
			continue
		}
		rel, blockedBy, err := s.repo.RelationOf(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		if blockedBy || rel == RelationBlocked {
			continue
		}
		out = append(out, Contact{Hash: h, User: cards[id], Relation: rel})
		delete(found, h) // a hash sent twice is one match
	}
	return out, nil
}

// Result is a user with the caller's relation to them.
type Result struct {
	User     *socialv1.PublicUser
	Relation Relation
}

// Search finds users by username prefix, hiding blocks both ways.
func (s *Service) Search(ctx context.Context, userID uuid.UUID, prefix string, limit int) ([]Result, error) {
	if !s.searches.Allow(userID, s.now()) {
		return nil, ErrLimited
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	ids, err := s.repo.SearchUsernames(ctx, prefix, limit)
	if err != nil {
		return nil, err
	}
	cards, err := s.repo.PublicUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(ids))
	for _, id := range ids {
		if cards[id] == nil {
			continue
		}
		rel, blockedBy, err := s.repo.RelationOf(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		if blockedBy {
			continue
		}
		out = append(out, Result{User: cards[id], Relation: rel})
	}
	return out, nil
}

// Profile is a user's public profile as one viewer sees it.
type Profile struct {
	User        *socialv1.PublicUser
	Stats       Stats
	Relation    Relation // 0 when signed out
	MemberSince time.Time
}

// Trip visibilities (trip.Visibility) a viewer may see on a profile.
var (
	tripsForOwner    = []int32{1, 2, 3, 4}
	tripsForFriend   = []int32{2, 4}
	tripsForEveryone = []int32{4}
)

// Profile loads target's profile for viewer (uuid.Nil when signed out). A
// user who blocked the viewer is NotFound.
func (s *Service) Profile(ctx context.Context, viewer, target uuid.UUID) (*Profile, error) {
	cards, err := s.repo.PublicUsers(ctx, []uuid.UUID{target})
	if err != nil {
		return nil, err
	}
	card := cards[target]
	if card == nil {
		return nil, ErrNotFound
	}
	p := &Profile{User: card}
	vis := tripsForEveryone
	if viewer != uuid.Nil {
		rel, blockedBy, err := s.repo.RelationOf(ctx, viewer, target)
		if err != nil {
			return nil, err
		}
		if blockedBy {
			return nil, ErrNotFound
		}
		p.Relation = rel
		switch rel {
		case RelationSelf:
			vis = tripsForOwner
		case RelationFriends:
			vis = tripsForFriend
		}
	}
	if p.Stats, err = s.repo.Stats(ctx, target, vis); err != nil {
		return nil, err
	}
	if p.MemberSince, err = s.repo.MemberSince(ctx, target); err != nil {
		return nil, err
	}
	return p, nil
}

// windowLimiter allows `max` events per key per window, in memory. It is per
// instance, so across N instances the real limit is at most N×max: enough to
// stop a loop, not a determined abuser (the proxy's IP limits cover that).
type windowLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	events map[uuid.UUID][]time.Time
}

func newWindowLimiter(maxEvents int, window time.Duration) *windowLimiter {
	return &windowLimiter{max: maxEvents, window: window, events: map[uuid.UUID][]time.Time{}}
}

func (l *windowLimiter) Allow(key uuid.UUID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	kept := l.events[key][:0]
	for _, t := range l.events[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.events[key] = kept
		return false
	}
	l.events[key] = append(kept, now)
	return true
}
