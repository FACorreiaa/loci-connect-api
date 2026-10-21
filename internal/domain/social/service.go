package social

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

// InviteOrigin is where an invite link opens: the web app's /invite/{code},
// which the iOS app also claims as a Universal Link.
const InviteOrigin = "https://lociai.fyi"

// ShareText is the message sent with an invite link, without the url.
const ShareText = "I'm using Loci to plan places that fit. Join me:"

// maxInviteCodeLen bounds a code taken from a signup request, which carries
// no validation so that a bad code never fails the signup.
const maxInviteCodeLen = 64

// Limits on actions a person could use to spam or to probe who is on Loci.
const (
	maxFollowsPerDay      = 100
	maxContactMatchPerDay = 5
	maxSearchesPerHour    = 100
	// oneWayAsRequests caps how many unreturned follows the older friend
	// RPCs list as requests.
	oneWayAsRequests = 100
	defaultPageSize  = 50
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
	// NewFollower tells `to` that `from` follows them; friends is true when
	// `to` already followed `from`, so the two are now friends.
	NewFollower(ctx context.Context, to uuid.UUID, from *socialv1.PublicUser, friends bool)
	// FollowRequested tells a private account's owner that `from` asked to
	// follow them.
	FollowRequested(ctx context.Context, to uuid.UUID, from *socialv1.PublicUser)
	// FollowApproved tells `to` that `by` approved their request.
	FollowApproved(ctx context.Context, to uuid.UUID, by *socialv1.PublicUser)
}

type noopNotifier struct{}

func (noopNotifier) NewFollower(context.Context, uuid.UUID, *socialv1.PublicUser, bool) {}
func (noopNotifier) FollowRequested(context.Context, uuid.UUID, *socialv1.PublicUser)   {}
func (noopNotifier) FollowApproved(context.Context, uuid.UUID, *socialv1.PublicUser)    {}

// Service is the people layer's logic.
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

// The service is the graph other packages read (trip sharing, rankings,
// boards). Friends are mutual follows.

// Relation reports whether a and b are friends and whether either blocked the
// other.
func (s *Service) Relation(ctx context.Context, a, b uuid.UUID) (bool, bool, error) {
	return s.repo.Relation(ctx, a, b)
}

// FriendIDs lists a user's friends (mutual follows).
func (s *Service) FriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.repo.FriendIDs(ctx, userID)
}

// FollowingIDs lists the people a user follows.
func (s *Service) FollowingIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.repo.FollowingIDs(ctx, userID)
}

// FollowerIDs lists the people who follow a user.
func (s *Service) FollowerIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.repo.FollowerIDs(ctx, userID)
}

func (s *Service) PublicUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	return s.repo.PublicUsers(ctx, ids)
}

// CanView reports whether viewer (uuid.Nil when signed out) may see owner's
// content, and whether either blocked the other.
func (s *Service) CanView(ctx context.Context, viewer, owner uuid.UUID) (visible, blocked bool, err error) {
	e, err := s.edge(ctx, viewer, owner)
	if errors.Is(err, ErrNotFound) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	if e.BlockedBy || e.State == FollowBlocked {
		return false, true, nil
	}
	return e.CanView(), false, nil
}

// edge is viewer's edge to other; NotFound when other does not exist.
func (s *Service) edge(ctx context.Context, viewer, other uuid.UUID) (Edge, error) {
	edges, err := s.repo.Edges(ctx, viewer, []uuid.UUID{other})
	if err != nil {
		return Edge{}, err
	}
	e, ok := edges[other]
	if !ok {
		return Edge{}, ErrNotFound
	}
	return e, nil
}

func (s *Service) card(ctx context.Context, id uuid.UUID) *socialv1.PublicUser {
	cards, err := s.repo.PublicUsers(ctx, []uuid.UUID{id})
	if err != nil {
		s.log.WarnContext(ctx, "load user card", "user_id", id, "error", err)
		return nil
	}
	return cards[id]
}

func newCode() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// InviteURL is the link an invite code opens.
func InviteURL(code string) string { return InviteOrigin + "/invite/" + code }

// MyInvite returns the caller's invite, minting one on first use or when a
// code from before codes became permanent has run out.
func (s *Service) MyInvite(ctx context.Context, userID uuid.UUID) (*Invite, error) {
	inv, err := s.repo.InviteFor(ctx, userID)
	if err == nil && s.live(inv) {
		return inv, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.RotateInvite(ctx, userID)
}

// RotateInvite replaces the caller's code; the old link stops working. The
// new code does not expire.
func (s *Service) RotateInvite(ctx context.Context, userID uuid.UUID) (*Invite, error) {
	inv := Invite{UserID: userID, Code: newCode()}
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
	if !s.live(inv) {
		return nil, ErrNotFound
	}
	return inv, nil
}

func (s *Service) live(inv *Invite) bool {
	return inv.ExpiresAt == nil || inv.ExpiresAt.After(s.now())
}

// OnSignup runs once a new account exists: it gives the account its own
// invite code and, when the signup came from someone's invite link, records
// that person as the inviter. Best effort: it never fails the signup, and a
// missing, unknown or self code is ignored.
func (s *Service) OnSignup(ctx context.Context, userID uuid.UUID, inviteCode string) {
	if _, err := s.MyInvite(ctx, userID); err != nil {
		s.log.WarnContext(ctx, "invite: mint code at signup", "user_id", userID, "error", err)
	}
	if inviteCode == "" || len(inviteCode) > maxInviteCodeLen {
		return
	}
	inv, err := s.LookupInvite(ctx, inviteCode)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.WarnContext(ctx, "invite: look up signup code", "user_id", userID, "error", err)
		}
		return
	}
	if inv.UserID == userID {
		return
	}
	set, err := s.repo.SetInvitedBy(ctx, userID, inv.UserID)
	if err != nil {
		s.log.WarnContext(ctx, "invite: record inviter", "user_id", userID, "error", err)
		return
	}
	if set {
		s.log.InfoContext(ctx, "invite attributed", "inviter_id", inv.UserID, "invitee_id", userID)
	}
}

// AcceptInvite makes the caller and the inviter friends: each follows the
// other at once, whatever their privacy. A blocked pair is NotFound, as if the
// invite did not exist.
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
	s.announceFollower(ctx, userID, inv.UserID, cards[userID], true)
	return cards[inv.UserID], s.now(), nil
}

// Follow makes from follow to, or asks to when to is private. It returns the
// resulting state, whether to already follows from, and the id of the follow
// or the request. Following again changes nothing and announces nothing.
func (s *Service) Follow(ctx context.Context, from, to uuid.UUID) (FollowState, bool, uuid.UUID, error) {
	if from == to {
		return 0, false, uuid.Nil, ErrSelf
	}
	e, err := s.edge(ctx, from, to)
	if err != nil {
		return 0, false, uuid.Nil, err
	}
	if e.BlockedBy || e.State == FollowBlocked {
		return 0, false, uuid.Nil, ErrNotFound
	}
	already := e.State == FollowFollowing || e.State == FollowRequested
	if !already {
		n, err := s.repo.CountFollowsSince(ctx, from, s.now().Add(-24*time.Hour))
		if err != nil {
			return 0, false, uuid.Nil, err
		}
		if n >= maxFollowsPerDay {
			return 0, false, uuid.Nil, ErrLimited
		}
	}
	state, id, err := s.repo.Follow(ctx, from, to)
	if err != nil {
		return 0, false, uuid.Nil, err
	}
	if !already {
		card := s.card(ctx, from)
		switch state {
		case FollowFollowing:
			s.announceFollower(ctx, from, to, card, e.FollowsYou)
		case FollowRequested:
			s.notify.FollowRequested(ctx, to, card)
		}
	}
	return state, e.FollowsYou, id, nil
}

// announceFollower pushes a new follower to `to`, at most once a day per
// follower, so following and unfollowing in a loop cannot spam anyone.
func (s *Service) announceFollower(ctx context.Context, from, to uuid.UUID, card *socialv1.PublicUser, friends bool) {
	ok, err := s.repo.ClaimPushSlot(ctx, to, "follow:"+from.String())
	if err != nil {
		s.log.WarnContext(ctx, "claim follow push slot", "user_id", to, "error", err)
		return
	}
	if ok {
		s.notify.NewFollower(ctx, to, card, friends)
	}
}

// Unfollow stops from following to, or withdraws from's pending request.
func (s *Service) Unfollow(ctx context.Context, from, to uuid.UUID) error {
	if from == to {
		return ErrSelf
	}
	return s.repo.Unfollow(ctx, from, to)
}

// RemoveFollower makes follower stop following owner.
func (s *Service) RemoveFollower(ctx context.Context, owner, follower uuid.UUID) error {
	if owner == follower {
		return ErrSelf
	}
	return s.repo.Unfollow(ctx, follower, owner)
}

// RespondFollow approves or declines a request to follow the caller.
func (s *Service) RespondFollow(ctx context.Context, userID, requestID uuid.UUID, approve bool) error {
	from, err := s.repo.RespondRequest(ctx, requestID, userID, approve)
	if err != nil {
		return err
	}
	if approve {
		s.notify.FollowApproved(ctx, from, s.card(ctx, userID))
	}
	return nil
}

// followBack makes userID follow other as the answer to other's follow (or
// request), announcing it when it is new.
func (s *Service) followBack(ctx context.Context, userID, other uuid.UUID) error {
	_, _, _, err := s.Follow(ctx, userID, other)
	if errors.Is(err, ErrLimited) {
		// Answering someone is never limited: follow without the count.
		_, _, err = s.repo.Follow(ctx, userID, other)
	}
	return err
}

// The friend RPCs below serve older clients from the follow graph.

// SendRequest is a friend request: a follow. The two are friends at once when
// target already follows the caller.
func (s *Service) SendRequest(ctx context.Context, from, to uuid.UUID) (Relation, uuid.UUID, error) {
	state, followsYou, id, err := s.Follow(ctx, from, to)
	if err != nil {
		return 0, uuid.Nil, err
	}
	if state == FollowFollowing && followsYou {
		return RelationFriends, id, nil
	}
	return RelationRequested, id, nil
}

// Respond answers a friend request addressed to the caller. requestID is a
// follow request, or a follow the caller has not returned. Accepting follows
// back; declining declines the request or removes the follower.
func (s *Service) Respond(ctx context.Context, userID, requestID uuid.UUID, accept bool) (*socialv1.PublicUser, error) {
	from, err := s.repo.RespondRequest(ctx, requestID, userID, accept)
	switch {
	case err == nil:
		if accept {
			s.notify.FollowApproved(ctx, from, s.card(ctx, userID))
		}
	case errors.Is(err, ErrNotFound):
		follower, followee, ferr := s.repo.FollowByID(ctx, requestID)
		if ferr != nil {
			return nil, ferr
		}
		if followee != userID {
			return nil, ErrNotFound
		}
		from = follower
		if !accept {
			return nil, s.repo.Unfollow(ctx, follower, userID)
		}
	default:
		return nil, err
	}
	if !accept {
		return nil, nil
	}
	if err := s.followBack(ctx, userID, from); err != nil {
		return nil, err
	}
	card := s.card(ctx, from)
	if card == nil {
		return nil, ErrNotFound
	}
	return card, nil
}

// Cancel withdraws a friend request the caller sent: a pending follow
// request, or a follow the other side has not returned.
func (s *Service) Cancel(ctx context.Context, userID, requestID uuid.UUID) error {
	err := s.repo.CancelRequest(ctx, requestID, userID)
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	follower, followee, err := s.repo.FollowByID(ctx, requestID)
	if err != nil {
		return err
	}
	if follower != userID {
		return ErrNotFound
	}
	return s.repo.Unfollow(ctx, userID, followee)
}

// ListFriendRequests lists, for older clients, pending follow requests plus
// follows that are not returned, newest first.
func (s *Service) ListFriendRequests(ctx context.Context, userID uuid.UUID, incoming bool) ([]Request, error) {
	reqs, err := s.repo.ListRequests(ctx, userID, incoming)
	if err != nil {
		return nil, err
	}
	oneWay, err := s.repo.ListOneWay(ctx, userID, incoming, oneWayAsRequests)
	if err != nil {
		return nil, err
	}
	out := append(reqs, oneWay...)
	slices.SortStableFunc(out, func(a, b Request) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// RemoveFriend ends following in both directions.
func (s *Service) RemoveFriend(ctx context.Context, a, b uuid.UUID) error {
	if a == b {
		return ErrSelf
	}
	if err := s.repo.Unfollow(ctx, a, b); err != nil {
		return err
	}
	return s.repo.Unfollow(ctx, b, a)
}

// Block ends following both ways and any pending request between the two.
func (s *Service) Block(ctx context.Context, blocker, blocked uuid.UUID) error {
	if blocker == blocked {
		return ErrSelf
	}
	return s.repo.Block(ctx, blocker, blocked)
}

// Settings are the caller's own social settings.
type Settings struct {
	Private bool
	Pending int32
}

// Settings reads the caller's privacy.
func (s *Service) Settings(ctx context.Context, userID uuid.UUID) (Settings, error) {
	private, pending, err := s.repo.Privacy(ctx, userID)
	return Settings{Private: private, Pending: pending}, err
}

// SetPrivate makes the caller's account private or public. Going public
// approves every pending request, quietly.
func (s *Service) SetPrivate(ctx context.Context, userID uuid.UUID, private bool) (Settings, error) {
	if _, err := s.repo.SetPrivate(ctx, userID, private); err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx, userID)
}

// FollowEntry is one person in a followers or following list, with the
// viewer's edge to them.
type FollowEntry struct {
	Follow
	Edge Edge
}

// ListFollows lists target's followers (or whom target follows) as viewer may
// see them. A private account's lists are ErrPrivate to anyone it has not
// approved; a user who blocked the viewer is NotFound. pageToken is an offset.
func (s *Service) ListFollows(ctx context.Context, viewer, target uuid.UUID, followers bool, pageSize int, pageToken string) ([]FollowEntry, string, int, error) {
	if target == uuid.Nil {
		target = viewer
	}
	e, err := s.edge(ctx, viewer, target)
	if err != nil {
		return nil, "", 0, err
	}
	if e.BlockedBy {
		return nil, "", 0, ErrNotFound
	}
	if !e.CanView() {
		return nil, "", 0, ErrPrivate
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = defaultPageSize
	}
	offset, _ := strconv.Atoi(pageToken)
	if offset < 0 {
		offset = 0
	}
	list, total, err := s.repo.ListFollows(ctx, target, followers, pageSize, offset)
	if err != nil {
		return nil, "", 0, err
	}
	ids := make([]uuid.UUID, len(list))
	for i, f := range list {
		ids[i] = parseCardID(f.User)
	}
	edges, err := s.repo.Edges(ctx, viewer, ids)
	if err != nil {
		return nil, "", 0, err
	}
	out := make([]FollowEntry, 0, len(list))
	for i, f := range list {
		edge := edges[ids[i]]
		if edge.BlockedBy || edge.State == FollowBlocked {
			continue
		}
		out = append(out, FollowEntry{Follow: f, Edge: edge})
	}
	next := ""
	if offset+len(list) < total {
		next = strconv.Itoa(offset + len(list))
	}
	return out, next, total, nil
}

func parseCardID(u *socialv1.PublicUser) uuid.UUID {
	id, _ := uuid.Parse(u.GetId())
	return id
}

// Contact is one matched contact.
type Contact struct {
	Hash string
	User *socialv1.PublicUser
	Edge Edge
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
	cards, edges, err := s.cardsAndEdges(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(found))
	for _, h := range hashes {
		id, ok := found[h]
		if !ok || id == userID || cards[id] == nil {
			continue
		}
		e := edges[id]
		if e.BlockedBy || e.State == FollowBlocked {
			continue
		}
		out = append(out, Contact{Hash: h, User: cards[id], Edge: e})
		delete(found, h) // a hash sent twice is one match
	}
	return out, nil
}

func (s *Service) cardsAndEdges(ctx context.Context, viewer uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, map[uuid.UUID]Edge, error) {
	cards, err := s.repo.PublicUsers(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	edges, err := s.repo.Edges(ctx, viewer, ids)
	if err != nil {
		return nil, nil, err
	}
	return cards, edges, nil
}

// ErrNotLinked is MatchFacebookFriends for a caller who never linked Facebook.
var ErrNotLinked = errors.New("connect Facebook first")

// FacebookFriends finds Loci users among the Facebook friends the caller's
// link granted, hiding blocks both ways.
func (s *Service) FacebookFriends(ctx context.Context, userID uuid.UUID) ([]Result, error) {
	ids, linked, err := s.repo.FacebookFriends(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !linked {
		return nil, ErrNotLinked
	}
	return s.results(ctx, userID, ids, true)
}

// results is ids as cards with the viewer's edge, dropping blocks (only the
// other side's when hideOwnBlocks is false).
func (s *Service) results(ctx context.Context, viewer uuid.UUID, ids []uuid.UUID, hideOwnBlocks bool) ([]Result, error) {
	cards, edges, err := s.cardsAndEdges(ctx, viewer, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(ids))
	for _, id := range ids {
		e := edges[id]
		if cards[id] == nil || e.BlockedBy || (hideOwnBlocks && e.State == FollowBlocked) {
			continue
		}
		out = append(out, Result{User: cards[id], Edge: e})
	}
	return out, nil
}

// Result is a user with the caller's edge to them.
type Result struct {
	User *socialv1.PublicUser
	Edge Edge
}

// Search finds users by username prefix, hiding users who blocked the caller.
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
	return s.results(ctx, userID, ids, false)
}

// Profile is a user's public profile as one viewer sees it.
type Profile struct {
	User        *socialv1.PublicUser
	Stats       Stats
	Edge        Edge // zero when signed out
	SignedIn    bool
	MemberSince time.Time
}

// CanView reports whether the viewer may see the profile's trips,
// achievements and follow lists.
func (p *Profile) CanView() bool {
	if !p.SignedIn {
		return !p.Edge.Private
	}
	return p.Edge.CanView()
}

// Trip visibilities (trip.Visibility) a viewer may see on a profile.
var (
	tripsForOwner    = []int32{1, 2, 3, 4}
	tripsForFriend   = []int32{2, 4}
	tripsForEveryone = []int32{4}
)

// Profile loads target's profile for viewer (uuid.Nil when signed out). A
// user who blocked the viewer is NotFound. A private account the viewer may
// not see keeps its card and follow counts; its travel counts are zero.
func (s *Service) Profile(ctx context.Context, viewer, target uuid.UUID) (*Profile, error) {
	cards, err := s.repo.PublicUsers(ctx, []uuid.UUID{target})
	if err != nil {
		return nil, err
	}
	card := cards[target]
	if card == nil {
		return nil, ErrNotFound
	}
	e, err := s.edge(ctx, viewer, target)
	if err != nil {
		return nil, err
	}
	if e.BlockedBy {
		return nil, ErrNotFound
	}
	p := &Profile{User: card, Edge: e, SignedIn: viewer != uuid.Nil}
	vis := tripsForEveryone
	switch {
	case !p.CanView():
		vis = nil
	case e.State == FollowSelf:
		vis = tripsForOwner
	case p.SignedIn && e.Friends():
		vis = tripsForFriend
	}
	if p.Stats, err = s.repo.Stats(ctx, target, vis); err != nil {
		return nil, err
	}
	if !p.CanView() {
		p.Stats.Cities, p.Stats.Countries = 0, 0
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
