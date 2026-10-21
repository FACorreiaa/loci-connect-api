package social

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	socialv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social"
	"github.com/google/uuid"
)

type pair = [2]uuid.UUID

// fakeRepo is an in-memory Repository: enough state to exercise the
// service's rules without a database.
type fakeRepo struct {
	users     map[uuid.UUID]*socialv1.PublicUser
	follows   map[pair]uuid.UUID // follower, followee -> follow id
	requests  map[pair]uuid.UUID // from, to -> pending request id
	private   map[uuid.UUID]bool
	blocks    map[pair]bool // blocker, blocked
	sentToday int
	slots     map[string]bool
	invites   map[string]Invite
	invitedBy map[uuid.UUID]uuid.UUID
	hashes    map[string]uuid.UUID
	stats     Stats
	statsVis  []int32
	// fbLinked users linked Facebook; fbFriends is who each link found.
	fbLinked  map[uuid.UUID]bool
	fbFriends map[uuid.UUID][]uuid.UUID
}

func newFake(ids ...uuid.UUID) *fakeRepo {
	f := &fakeRepo{
		users: map[uuid.UUID]*socialv1.PublicUser{}, follows: map[pair]uuid.UUID{},
		requests: map[pair]uuid.UUID{}, private: map[uuid.UUID]bool{},
		blocks: map[pair]bool{}, slots: map[string]bool{},
		invites: map[string]Invite{}, hashes: map[string]uuid.UUID{},
		invitedBy: map[uuid.UUID]uuid.UUID{},
	}
	for _, id := range ids {
		f.users[id] = &socialv1.PublicUser{Id: id.String(), Username: "u" + id.String()[:4], DisplayName: "User"}
	}
	return f
}

func (f *fakeRepo) follow(a, b uuid.UUID) {
	if f.follows[pair{a, b}] == uuid.Nil {
		f.follows[pair{a, b}] = uuid.New()
	}
}

func (f *fakeRepo) befriend(a, b uuid.UUID) { f.follow(a, b); f.follow(b, a) }

func (f *fakeRepo) PublicUsers(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*socialv1.PublicUser, error) {
	out := map[uuid.UUID]*socialv1.PublicUser{}
	for _, id := range ids {
		if u := f.users[id]; u != nil {
			out[id] = u
		}
	}
	return out, nil
}

func (f *fakeRepo) UserIDByUsername(context.Context, string) (uuid.UUID, error) {
	return uuid.Nil, ErrNotFound
}

func (f *fakeRepo) MemberSince(context.Context, uuid.UUID) (time.Time, error) {
	return time.Unix(0, 0), nil
}

func (f *fakeRepo) Relation(_ context.Context, a, b uuid.UUID) (bool, bool, error) {
	friends := f.follows[pair{a, b}] != uuid.Nil && f.follows[pair{b, a}] != uuid.Nil
	return friends, f.blocks[pair{a, b}] || f.blocks[pair{b, a}], nil
}

func (f *fakeRepo) Edges(_ context.Context, v uuid.UUID, others []uuid.UUID) (map[uuid.UUID]Edge, error) {
	out := map[uuid.UUID]Edge{}
	for _, o := range others {
		if f.users[o] == nil {
			continue
		}
		e := Edge{
			FollowsYou: f.follows[pair{o, v}] != uuid.Nil,
			Incoming:   f.requests[pair{o, v}] != uuid.Nil,
			BlockedBy:  f.blocks[pair{o, v}],
			Private:    f.private[o],
		}
		switch {
		case o == v:
			e = Edge{State: FollowSelf, Private: f.private[o]}
		case f.blocks[pair{v, o}]:
			e.State = FollowBlocked
		case f.follows[pair{v, o}] != uuid.Nil:
			e.State = FollowFollowing
		case f.requests[pair{v, o}] != uuid.Nil:
			e.State = FollowRequested
		default:
			e.State = FollowNone
		}
		out[o] = e
	}
	return out, nil
}

func (f *fakeRepo) FriendIDs(context.Context, uuid.UUID) ([]uuid.UUID, error)    { return nil, nil }
func (f *fakeRepo) FollowingIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, nil }
func (f *fakeRepo) FollowerIDs(context.Context, uuid.UUID) ([]uuid.UUID, error)  { return nil, nil }
func (f *fakeRepo) ListFriends(context.Context, uuid.UUID) ([]Friend, error)     { return nil, nil }

func (f *fakeRepo) ListFollows(_ context.Context, user uuid.UUID, followers bool, _, _ int) ([]Follow, int, error) {
	var out []Follow
	for k := range f.follows {
		switch {
		case followers && k[1] == user:
			out = append(out, Follow{User: f.users[k[0]]})
		case !followers && k[0] == user:
			out = append(out, Follow{User: f.users[k[1]]})
		}
	}
	return out, len(out), nil
}

func (f *fakeRepo) Follow(_ context.Context, from, to uuid.UUID) (FollowState, uuid.UUID, error) {
	if id := f.follows[pair{from, to}]; id != uuid.Nil {
		return FollowFollowing, id, nil
	}
	if f.private[to] {
		if f.requests[pair{from, to}] == uuid.Nil {
			f.requests[pair{from, to}] = uuid.New()
			f.sentToday++
		}
		return FollowRequested, f.requests[pair{from, to}], nil
	}
	f.follow(from, to)
	f.sentToday++
	return FollowFollowing, f.follows[pair{from, to}], nil
}

func (f *fakeRepo) Unfollow(_ context.Context, from, to uuid.UUID) error {
	delete(f.follows, pair{from, to})
	delete(f.requests, pair{from, to})
	return nil
}

func (f *fakeRepo) FollowByID(_ context.Context, id uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	for k, v := range f.follows {
		if v == id {
			return k[0], k[1], nil
		}
	}
	return uuid.Nil, uuid.Nil, ErrNotFound
}

func (f *fakeRepo) RespondRequest(_ context.Context, id, to uuid.UUID, approve bool) (uuid.UUID, error) {
	for k, v := range f.requests {
		if v == id && k[1] == to {
			delete(f.requests, k)
			if approve {
				f.follow(k[0], to)
			}
			return k[0], nil
		}
	}
	return uuid.Nil, ErrNotFound
}

func (f *fakeRepo) CancelRequest(_ context.Context, id, from uuid.UUID) error {
	for k, v := range f.requests {
		if v == id && k[0] == from {
			delete(f.requests, k)
			return nil
		}
	}
	return ErrNotFound
}

func (f *fakeRepo) ListRequests(_ context.Context, user uuid.UUID, incoming bool) ([]Request, error) {
	var out []Request
	for k, id := range f.requests {
		if (incoming && k[1] == user) || (!incoming && k[0] == user) {
			out = append(out, Request{ID: id, From: f.users[k[0]], To: f.users[k[1]]})
		}
	}
	return out, nil
}

func (f *fakeRepo) ListOneWay(_ context.Context, user uuid.UUID, incoming bool, _ int) ([]Request, error) {
	var out []Request
	for k, id := range f.follows {
		if f.follows[pair{k[1], k[0]}] != uuid.Nil {
			continue
		}
		if (incoming && k[1] == user) || (!incoming && k[0] == user) {
			out = append(out, Request{ID: id, From: f.users[k[0]], To: f.users[k[1]]})
		}
	}
	return out, nil
}

func (f *fakeRepo) CountFollowsSince(context.Context, uuid.UUID, time.Time) (int, error) {
	return f.sentToday, nil
}

func (f *fakeRepo) MakeFriends(_ context.Context, a, b uuid.UUID) error {
	f.befriend(a, b)
	delete(f.requests, pair{a, b})
	delete(f.requests, pair{b, a})
	return nil
}

func (f *fakeRepo) Privacy(_ context.Context, id uuid.UUID) (bool, int32, error) {
	var n int32
	for k := range f.requests {
		if k[1] == id {
			n++
		}
	}
	return f.private[id], n, nil
}

func (f *fakeRepo) SetPrivate(_ context.Context, id uuid.UUID, private bool) ([]uuid.UUID, error) {
	f.private[id] = private
	var approved []uuid.UUID
	if !private {
		for k := range f.requests {
			if k[1] == id {
				f.follow(k[0], id)
				delete(f.requests, k)
				approved = append(approved, k[0])
			}
		}
	}
	return approved, nil
}

func (f *fakeRepo) Block(_ context.Context, a, b uuid.UUID) error {
	f.blocks[pair{a, b}] = true
	for _, k := range []pair{{a, b}, {b, a}} {
		delete(f.follows, k)
		delete(f.requests, k)
	}
	return nil
}

func (f *fakeRepo) Unblock(_ context.Context, a, b uuid.UUID) error {
	delete(f.blocks, pair{a, b})
	return nil
}

func (f *fakeRepo) InviteFor(_ context.Context, uid uuid.UUID) (*Invite, error) {
	for _, inv := range f.invites {
		if inv.UserID == uid {
			i := inv
			return &i, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) SaveInvite(_ context.Context, inv Invite) error {
	for code, old := range f.invites {
		if old.UserID == inv.UserID {
			delete(f.invites, code)
		}
	}
	f.invites[inv.Code] = inv
	return nil
}

func (f *fakeRepo) InviteByCode(_ context.Context, code string) (*Invite, error) {
	inv, ok := f.invites[code]
	if !ok {
		return nil, ErrNotFound
	}
	return &inv, nil
}

func (f *fakeRepo) SetInvitedBy(_ context.Context, invitee, inviter uuid.UUID) (bool, error) {
	if _, ok := f.invitedBy[invitee]; ok || invitee == inviter {
		return false, nil
	}
	f.invitedBy[invitee] = inviter
	return true, nil
}

func (f *fakeRepo) MatchHashes(_ context.Context, hs []string) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	for _, h := range hs {
		if id, ok := f.hashes[h]; ok {
			out[h] = id
		}
	}
	return out, nil
}

func (f *fakeRepo) FacebookFriends(_ context.Context, id uuid.UUID) ([]uuid.UUID, bool, error) {
	return f.fbFriends[id], f.fbLinked[id], nil
}

func (f *fakeRepo) SearchUsernames(context.Context, string, int) ([]uuid.UUID, error) {
	return nil, nil
}

func (f *fakeRepo) Stats(_ context.Context, _ uuid.UUID, vis []int32) (Stats, error) {
	f.statsVis = vis
	return f.stats, nil
}

func (f *fakeRepo) ClaimPushSlot(_ context.Context, id uuid.UUID, kind string) (bool, error) {
	k := id.String() + kind
	if f.slots[k] {
		return false, nil
	}
	f.slots[k] = true
	return true, nil
}

type recordingNotifier struct {
	followers, friends, requested, approved []uuid.UUID
}

func (r *recordingNotifier) NewFollower(_ context.Context, to uuid.UUID, _ *socialv1.PublicUser, friends bool) {
	if friends {
		r.friends = append(r.friends, to)
		return
	}
	r.followers = append(r.followers, to)
}

func (r *recordingNotifier) FollowRequested(_ context.Context, to uuid.UUID, _ *socialv1.PublicUser) {
	r.requested = append(r.requested, to)
}

func (r *recordingNotifier) FollowApproved(_ context.Context, to uuid.UUID, _ *socialv1.PublicUser) {
	r.approved = append(r.approved, to)
}

func newSvc(f *fakeRepo) (*Service, *recordingNotifier) {
	n := &recordingNotifier{}
	return NewService(f, n, nil), n
}

func TestFollowPublicAndPrivate(t *testing.T) {
	ctx := context.Background()
	ana, rui, eva := uuid.New(), uuid.New(), uuid.New()
	f := newFake(ana, rui, eva)
	f.private[eva] = true
	svc, n := newSvc(f)

	if _, _, _, err := svc.Follow(ctx, ana, ana); !errors.Is(err, ErrSelf) {
		t.Fatalf("follow self: err = %v", err)
	}

	state, followsYou, _, err := svc.Follow(ctx, ana, rui)
	if err != nil || state != FollowFollowing || followsYou {
		t.Fatalf("follow public: state=%v followsYou=%v err=%v", state, followsYou, err)
	}
	if len(n.followers) != 1 || n.followers[0] != rui {
		t.Fatalf("followee not told: %v", n.followers)
	}
	// Following again is a no-op and announces nothing.
	if state, _, _, _ := svc.Follow(ctx, ana, rui); state != FollowFollowing || len(n.followers) != 1 {
		t.Fatalf("repeat follow: state=%v pushes=%d", state, len(n.followers))
	}
	// Rui follows back: they are friends, and Ana hears it as such.
	state, followsYou, _, _ = svc.Follow(ctx, rui, ana)
	if state != FollowFollowing || !followsYou || len(n.friends) != 1 || n.friends[0] != ana {
		t.Fatalf("follow back: state=%v followsYou=%v friends=%v", state, followsYou, n.friends)
	}
	if friends, _, _ := f.Relation(ctx, ana, rui); !friends {
		t.Fatal("mutual follow is not a friendship")
	}

	// A private account gets a request, not a follower.
	state, _, reqID, _ := svc.Follow(ctx, ana, eva)
	if state != FollowRequested || len(n.requested) != 1 || n.requested[0] != eva {
		t.Fatalf("follow private: state=%v requested=%v", state, n.requested)
	}
	if err := svc.RespondFollow(ctx, ana, reqID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the sender approved their own request: err = %v", err)
	}
	if err := svc.RespondFollow(ctx, eva, reqID, true); err != nil {
		t.Fatal(err)
	}
	if f.follows[pair{ana, eva}] == uuid.Nil || len(n.approved) != 1 || n.approved[0] != ana {
		t.Fatalf("approval did not make a follower or was not announced: %v", n.approved)
	}
}

func TestFollowPushOncePerDay(t *testing.T) {
	ctx := context.Background()
	ana, rui := uuid.New(), uuid.New()
	f := newFake(ana, rui)
	svc, n := newSvc(f)
	for range 3 {
		if _, _, _, err := svc.Follow(ctx, ana, rui); err != nil {
			t.Fatal(err)
		}
		if err := svc.Unfollow(ctx, ana, rui); err != nil {
			t.Fatal(err)
		}
	}
	if len(n.followers) != 1 {
		t.Fatalf("follow/unfollow loop pushed %d times, want 1", len(n.followers))
	}
}

func TestFollowHidesBlocksAndLimits(t *testing.T) {
	ctx := context.Background()
	ana, rui, eve := uuid.New(), uuid.New(), uuid.New()
	f := newFake(ana, rui, eve)
	svc, _ := newSvc(f)

	f.blocks[pair{rui, ana}] = true // Rui blocked Ana
	if _, _, _, err := svc.Follow(ctx, ana, rui); !errors.Is(err, ErrNotFound) {
		t.Fatalf("follow someone who blocked you: err = %v, want NotFound", err)
	}
	f.sentToday = maxFollowsPerDay
	if _, _, _, err := svc.Follow(ctx, ana, eve); !errors.Is(err, ErrLimited) {
		t.Fatalf("over the daily limit: err = %v", err)
	}
}

func TestBlockEndsFollowsBothWays(t *testing.T) {
	ctx := context.Background()
	ana, rui := uuid.New(), uuid.New()
	f := newFake(ana, rui)
	f.befriend(ana, rui)
	svc, _ := newSvc(f)
	if err := svc.Block(ctx, ana, rui); err != nil {
		t.Fatal(err)
	}
	if len(f.follows) != 0 {
		t.Fatalf("follows survived a block: %v", f.follows)
	}
}

// Older clients still speak friend requests: a follow is a request, a
// follower not followed back is an incoming request, accepting follows back.
func TestLegacyFriendRPCs(t *testing.T) {
	ctx := context.Background()
	ana, rui := uuid.New(), uuid.New()
	f := newFake(ana, rui)
	svc, _ := newSvc(f)

	rel, id, err := svc.SendRequest(ctx, ana, rui)
	if err != nil || rel != RelationRequested || id == uuid.Nil {
		t.Fatalf("send: rel=%v id=%v err=%v", rel, id, err)
	}
	incoming, _ := svc.ListFriendRequests(ctx, rui, true)
	if len(incoming) != 1 || incoming[0].ID != id || incoming[0].From.GetId() != ana.String() {
		t.Fatalf("Rui's incoming requests = %+v, want Ana's follow", incoming)
	}
	outgoing, _ := svc.ListFriendRequests(ctx, ana, false)
	if len(outgoing) != 1 || outgoing[0].ID != id {
		t.Fatalf("Ana's outgoing requests = %+v", outgoing)
	}
	if _, err := svc.Respond(ctx, ana, id, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the sender accepted their own request: err = %v", err)
	}
	friend, err := svc.Respond(ctx, rui, id, true)
	if err != nil || friend.GetId() != ana.String() {
		t.Fatalf("accept: friend=%v err=%v", friend, err)
	}
	if friends, _, _ := f.Relation(ctx, ana, rui); !friends {
		t.Fatal("accepting did not follow back")
	}
	if left, _ := svc.ListFriendRequests(ctx, rui, true); len(left) != 0 {
		t.Fatalf("friends still listed as requests: %+v", left)
	}

	// Declining a follower removes them; cancelling a follow unfollows.
	if err := svc.RemoveFriend(ctx, ana, rui); err != nil {
		t.Fatal(err)
	}
	_, id, _ = svc.SendRequest(ctx, ana, rui)
	if _, err := svc.Respond(ctx, rui, id, false); err != nil {
		t.Fatal(err)
	}
	if f.follows[pair{ana, rui}] != uuid.Nil {
		t.Fatal("declining did not remove the follower")
	}
	_, id, _ = svc.SendRequest(ctx, ana, rui)
	if err := svc.Cancel(ctx, rui, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the recipient cancelled the sender's follow: err = %v", err)
	}
	if err := svc.Cancel(ctx, ana, id); err != nil || f.follows[pair{ana, rui}] != uuid.Nil {
		t.Fatalf("cancel did not unfollow: err=%v", err)
	}
}

func TestEdgeLegacy(t *testing.T) {
	for _, c := range []struct {
		name string
		e    Edge
		want Relation
	}{
		{"self", Edge{State: FollowSelf}, RelationSelf},
		{"blocked", Edge{State: FollowBlocked, FollowsYou: true}, RelationBlocked},
		{"mutual", Edge{State: FollowFollowing, FollowsYou: true}, RelationFriends},
		{"I follow", Edge{State: FollowFollowing}, RelationRequested},
		{"I asked", Edge{State: FollowRequested}, RelationRequested},
		{"they follow", Edge{State: FollowNone, FollowsYou: true}, RelationIncoming},
		{"they asked", Edge{State: FollowNone, Incoming: true}, RelationIncoming},
		{"strangers", Edge{State: FollowNone}, RelationNone},
	} {
		if got := c.e.Legacy(); got != c.want {
			t.Errorf("%s: Legacy() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrivacy(t *testing.T) {
	ctx := context.Background()
	ana, rui, eva := uuid.New(), uuid.New(), uuid.New()
	f := newFake(ana, rui, eva)
	svc, _ := newSvc(f)
	if _, err := svc.SetPrivate(ctx, eva, true); err != nil {
		t.Fatal(err)
	}
	_, _, _, _ = svc.Follow(ctx, ana, eva)
	_, _, _, _ = svc.Follow(ctx, rui, eva)
	if _, _, _, err := svc.ListFollows(ctx, ana, eva, true, 0, ""); !errors.Is(err, ErrPrivate) {
		t.Fatalf("a private account's followers were listed to a stranger: err = %v", err)
	}
	set, _ := svc.Settings(ctx, eva)
	if !set.Private || set.Pending != 2 {
		t.Fatalf("settings = %+v, want private with 2 pending", set)
	}
	// Going public approves everyone waiting.
	set, _ = svc.SetPrivate(ctx, eva, false)
	if set.Private || set.Pending != 0 || f.follows[pair{ana, eva}] == uuid.Nil || f.follows[pair{rui, eva}] == uuid.Nil {
		t.Fatalf("going public did not approve pending requests: %+v", set)
	}
	list, _, total, err := svc.ListFollows(ctx, ana, eva, true, 0, "")
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("followers = %d/%d err=%v", len(list), total, err)
	}
}

func TestInvites(t *testing.T) {
	ctx := context.Background()
	ana, rui := uuid.New(), uuid.New()
	f := newFake(ana, rui)
	svc, n := newSvc(f)
	now := time.Now()
	svc.now = func() time.Time { return now }

	inv, err := svc.MyInvite(ctx, ana)
	if err != nil || inv.Code == "" {
		t.Fatalf("MyInvite: %v", err)
	}
	again, _ := svc.MyInvite(ctx, ana)
	if again.Code != inv.Code {
		t.Fatal("a live invite was replaced")
	}

	if _, _, err := svc.AcceptInvite(ctx, ana, inv.Code); !errors.Is(err, ErrSelf) {
		t.Fatalf("accepting your own invite: err = %v", err)
	}
	inviter, _, err := svc.AcceptInvite(ctx, rui, inv.Code)
	if err != nil || inviter.GetId() != ana.String() {
		t.Fatalf("accept invite: %v %v", inviter, err)
	}
	if friends, _, _ := f.Relation(ctx, rui, ana); !friends {
		t.Fatal("an accepted invite did not make friends")
	}
	if len(n.friends) != 1 || n.friends[0] != ana {
		t.Fatalf("inviter not told: %v", n.friends)
	}

	// Codes do not expire: a link sent years ago still opens.
	svc.now = func() time.Time { return now.Add(5 * 365 * 24 * time.Hour) }
	if _, err := svc.LookupInvite(ctx, inv.Code); err != nil {
		t.Fatalf("a permanent code stopped working: %v", err)
	}
	if later, _ := svc.MyInvite(ctx, ana); later.Code != inv.Code {
		t.Fatal("a permanent code was replaced")
	}

	rotated, _ := svc.RotateInvite(ctx, ana)
	if rotated.ExpiresAt != nil {
		t.Fatal("a rotated code expires")
	}
	if _, err := svc.LookupInvite(ctx, inv.Code); !errors.Is(err, ErrNotFound) {
		t.Fatal("a rotated code still works")
	}
	if _, err := svc.LookupInvite(ctx, rotated.Code); err != nil {
		t.Fatalf("the rotated-in code does not work: %v", err)
	}
}

// A code from before codes became permanent keeps its expiry, and is
// replaced once it runs out.
func TestInviteLegacyExpiry(t *testing.T) {
	ctx := context.Background()
	ana := uuid.New()
	f := newFake(ana)
	svc, _ := newSvc(f)
	now := time.Now()
	svc.now = func() time.Time { return now }
	past := now.Add(-time.Hour)
	f.invites["old"] = Invite{UserID: ana, Code: "old", ExpiresAt: &past}

	if _, err := svc.LookupInvite(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatal("an expired legacy code still works")
	}
	renewed, err := svc.MyInvite(ctx, ana)
	if err != nil || renewed.Code == "old" || renewed.ExpiresAt != nil {
		t.Fatalf("an expired legacy code was not replaced by a permanent one: %+v %v", renewed, err)
	}
}

func TestOnSignup(t *testing.T) {
	ctx := context.Background()
	ana, rui, eva := uuid.New(), uuid.New(), uuid.New()
	f := newFake(ana, rui, eva)
	svc, _ := newSvc(f)
	inv, _ := svc.MyInvite(ctx, ana)

	// No code, an unknown code, an oversized code: the account still gets
	// its own code, and nobody is recorded as its inviter.
	for _, code := range []string{"", "nope", strings.Repeat("x", maxInviteCodeLen+1)} {
		svc.OnSignup(ctx, eva, code)
	}
	if _, ok := f.invitedBy[eva]; ok {
		t.Fatal("an inviter was recorded from a bad code")
	}
	if own, err := f.InviteFor(ctx, eva); err != nil || own.Code == "" {
		t.Fatal("a new account got no invite code")
	}

	// Your own code is not an invite.
	svc.OnSignup(ctx, ana, inv.Code)
	if _, ok := f.invitedBy[ana]; ok {
		t.Fatal("an account was recorded as inviting itself")
	}

	svc.OnSignup(ctx, rui, inv.Code)
	if f.invitedBy[rui] != ana {
		t.Fatalf("inviter = %v, want %v", f.invitedBy[rui], ana)
	}
	// Written once: a later code does not move it.
	evaInv, _ := svc.MyInvite(ctx, eva)
	svc.OnSignup(ctx, rui, evaInv.Code)
	if f.invitedBy[rui] != ana {
		t.Fatal("the inviter was overwritten")
	}
}

func TestAcceptInviteBlocked(t *testing.T) {
	ctx := context.Background()
	ana, rui := uuid.New(), uuid.New()
	f := newFake(ana, rui)
	svc, _ := newSvc(f)
	inv, _ := svc.MyInvite(ctx, ana)
	f.blocks[pair{ana, rui}] = true
	if _, _, err := svc.AcceptInvite(ctx, rui, inv.Code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a blocked user accepted an invite: err = %v", err)
	}
}

func TestMatchContacts(t *testing.T) {
	ctx := context.Background()
	me, ana, rui, eve := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := newFake(me, ana, rui, eve)
	f.hashes = map[string]uuid.UUID{"h-me": me, "h-ana": ana, "h-rui": rui, "h-eve": eve}
	f.befriend(me, rui)
	f.blocks[pair{eve, me}] = true // Eve blocked me
	svc, _ := newSvc(f)

	got, err := svc.MatchContacts(ctx, me, []string{"h-me", "h-ana", "h-ana", "h-rui", "h-eve", "h-nobody"})
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]Relation{}
	for _, c := range got {
		rels[c.Hash] = c.Edge.Legacy()
	}
	if len(got) != 2 || rels["h-ana"] != RelationNone || rels["h-rui"] != RelationFriends {
		t.Fatalf("matches = %+v, want Ana (none) and Rui (friends) only", got)
	}

	for i := 1; i < maxContactMatchPerDay; i++ {
		if _, err := svc.MatchContacts(ctx, me, []string{"h-ana"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.MatchContacts(ctx, me, []string{"h-ana"}); !errors.Is(err, ErrLimited) {
		t.Fatalf("over the daily contact limit: err = %v", err)
	}
}

func TestProfileVisibility(t *testing.T) {
	ctx := context.Background()
	me, friend, fan, stranger, blocker := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := newFake(me, friend, fan, stranger, blocker)
	f.befriend(friend, me)
	f.follow(fan, me)                  // follows me; I do not follow back
	f.blocks[pair{blocker, me}] = true // `blocker` blocked me
	svc, _ := newSvc(f)

	eq := func(a, b []int32) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	for _, c := range []struct {
		name   string
		viewer uuid.UUID
		want   []int32
	}{
		{"owner", me, tripsForOwner},
		{"friend", friend, tripsForFriend},
		{"follower who is not a friend", fan, tripsForEveryone},
		{"stranger", stranger, tripsForEveryone},
		{"signed out", uuid.Nil, tripsForEveryone},
	} {
		if _, err := svc.Profile(ctx, c.viewer, me); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !eq(f.statsVis, c.want) {
			t.Errorf("%s counts trips %v, want %v", c.name, f.statsVis, c.want)
		}
	}
	if _, err := svc.Profile(ctx, me, blocker); !errors.Is(err, ErrNotFound) {
		t.Fatalf("saw the profile of someone who blocked me: err = %v", err)
	}

	// Private: followers see trips as before; strangers and signed-out
	// visitors see the card only.
	f.private[me] = true
	for _, c := range []struct {
		name    string
		viewer  uuid.UUID
		canView bool
	}{
		{"owner", me, true},
		{"follower", fan, true},
		{"stranger", stranger, false},
		{"signed out", uuid.Nil, false},
	} {
		p, err := svc.Profile(ctx, c.viewer, me)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.CanView() != c.canView {
			t.Errorf("%s: CanView = %v, want %v", c.name, p.CanView(), c.canView)
		}
		if !c.canView && len(f.statsVis) != 0 {
			t.Errorf("%s counts trips %v on a private profile", c.name, f.statsVis)
		}
	}
}

func TestWindowLimiter(t *testing.T) {
	l := newWindowLimiter(2, time.Hour)
	k := uuid.New()
	t0 := time.Now()
	first, second, third := l.Allow(k, t0), l.Allow(k, t0), l.Allow(k, t0)
	if !first || !second || third {
		t.Fatal("limiter did not stop the third event")
	}
	if !l.Allow(uuid.New(), t0) {
		t.Fatal("limiter shared state between keys")
	}
	if !l.Allow(k, t0.Add(61*time.Minute)) {
		t.Fatal("limiter did not free up after the window")
	}
}

func TestFacebookFriendsNeedsALinkAndHidesBlocks(t *testing.T) {
	me, friend, blocker := uuid.New(), uuid.New(), uuid.New()
	f := newFake(me, friend, blocker)
	svc := NewService(f, nil, nil)
	ctx := context.Background()
	if _, err := svc.FacebookFriends(ctx, me); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("unlinked: err = %v, want ErrNotLinked", err)
	}
	f.fbLinked = map[uuid.UUID]bool{me: true}
	f.fbFriends = map[uuid.UUID][]uuid.UUID{me: {friend, blocker}}
	f.blocks[pair{blocker, me}] = true
	got, err := svc.FacebookFriends(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].User.GetId() != friend.String() {
		t.Fatalf("matches = %+v, want only the friend who did not block me", got)
	}
}
