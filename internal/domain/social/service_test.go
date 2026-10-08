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

// fakeRepo is an in-memory Repository: enough state to exercise the
// service's rules without a database.
type fakeRepo struct {
	users     map[uuid.UUID]*socialv1.PublicUser
	friends   map[[2]uuid.UUID]bool
	blocks    map[[2]uuid.UUID]bool // blocker, blocked
	pending   map[[2]uuid.UUID]uuid.UUID
	sentToday int
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
		users: map[uuid.UUID]*socialv1.PublicUser{}, friends: map[[2]uuid.UUID]bool{},
		blocks: map[[2]uuid.UUID]bool{}, pending: map[[2]uuid.UUID]uuid.UUID{},
		invites: map[string]Invite{}, hashes: map[string]uuid.UUID{},
		invitedBy: map[uuid.UUID]uuid.UUID{},
	}
	for _, id := range ids {
		f.users[id] = &socialv1.PublicUser{Id: id.String(), Username: "u" + id.String()[:4], DisplayName: "User"}
	}
	return f
}

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
	return f.friends[[2]uuid.UUID{a, b}], f.blocks[[2]uuid.UUID{a, b}] || f.blocks[[2]uuid.UUID{b, a}], nil
}

func (f *fakeRepo) RelationOf(_ context.Context, v, o uuid.UUID) (Relation, bool, error) {
	blockedBy := f.blocks[[2]uuid.UUID{o, v}]
	switch {
	case v == o:
		return RelationSelf, false, nil
	case f.blocks[[2]uuid.UUID{v, o}]:
		return RelationBlocked, blockedBy, nil
	case f.friends[[2]uuid.UUID{v, o}]:
		return RelationFriends, blockedBy, nil
	case f.pending[[2]uuid.UUID{v, o}] != uuid.Nil:
		return RelationRequested, blockedBy, nil
	case f.pending[[2]uuid.UUID{o, v}] != uuid.Nil:
		return RelationIncoming, blockedBy, nil
	}
	return RelationNone, blockedBy, nil
}
func (f *fakeRepo) FriendIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, nil }
func (f *fakeRepo) ListFriends(context.Context, uuid.UUID) ([]Friend, error)  { return nil, nil }
func (f *fakeRepo) RemoveFriend(context.Context, uuid.UUID, uuid.UUID) error  { return nil }
func (f *fakeRepo) befriend(a, b uuid.UUID) {
	f.friends[[2]uuid.UUID{a, b}], f.friends[[2]uuid.UUID{b, a}] = true, true
	delete(f.pending, [2]uuid.UUID{a, b})
	delete(f.pending, [2]uuid.UUID{b, a})
}

func (f *fakeRepo) SendRequest(_ context.Context, from, to uuid.UUID) (uuid.UUID, bool, error) {
	if id := f.pending[[2]uuid.UUID{to, from}]; id != uuid.Nil {
		f.befriend(from, to)
		return id, true, nil
	}
	if f.pending[[2]uuid.UUID{from, to}] != uuid.Nil {
		return uuid.Nil, false, ErrConflict
	}
	id := uuid.New()
	f.pending[[2]uuid.UUID{from, to}] = id
	f.sentToday++
	return id, false, nil
}

func (f *fakeRepo) RespondRequest(_ context.Context, id, to uuid.UUID, accept bool) (uuid.UUID, error) {
	for k, v := range f.pending {
		if v == id && k[1] == to {
			if accept {
				f.befriend(k[0], to)
			} else {
				delete(f.pending, k)
			}
			return k[0], nil
		}
	}
	return uuid.Nil, ErrNotFound
}
func (f *fakeRepo) CancelRequest(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeRepo) ListRequests(context.Context, uuid.UUID, bool) ([]Request, error) {
	return nil, nil
}

func (f *fakeRepo) CountRequestsSince(context.Context, uuid.UUID, time.Time) (int, error) {
	return f.sentToday, nil
}

func (f *fakeRepo) MakeFriends(_ context.Context, a, b uuid.UUID) error { f.befriend(a, b); return nil }

func (f *fakeRepo) Block(_ context.Context, a, b uuid.UUID) error {
	f.blocks[[2]uuid.UUID{a, b}] = true
	delete(f.friends, [2]uuid.UUID{a, b})
	delete(f.friends, [2]uuid.UUID{b, a})
	return nil
}

func (f *fakeRepo) Unblock(_ context.Context, a, b uuid.UUID) error {
	delete(f.blocks, [2]uuid.UUID{a, b})
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

type recordingNotifier struct{ requests, accepts []uuid.UUID }

func (r *recordingNotifier) FriendRequest(_ context.Context, to uuid.UUID, _ *socialv1.PublicUser) {
	r.requests = append(r.requests, to)
}

func (r *recordingNotifier) FriendAccepted(_ context.Context, to uuid.UUID, _ *socialv1.PublicUser) {
	r.accepts = append(r.accepts, to)
}

func newSvc(f *fakeRepo) (*Service, *recordingNotifier) {
	n := &recordingNotifier{}
	return NewService(f, n, nil), n
}

func TestSendRequestFlow(t *testing.T) {
	ctx := context.Background()
	ana, rui := uuid.New(), uuid.New()
	f := newFake(ana, rui)
	svc, n := newSvc(f)

	if _, _, err := svc.SendRequest(ctx, ana, ana); !errors.Is(err, ErrSelf) {
		t.Fatalf("request to self: err = %v", err)
	}

	rel, id, err := svc.SendRequest(ctx, ana, rui)
	if err != nil || rel != RelationRequested || id == uuid.Nil {
		t.Fatalf("first request: rel=%v id=%v err=%v", rel, id, err)
	}
	if len(n.requests) != 1 || n.requests[0] != rui {
		t.Fatalf("recipient not notified: %v", n.requests)
	}

	// Asking again is idempotent and notifies nobody.
	if rel, _, _ := svc.SendRequest(ctx, ana, rui); rel != RelationRequested || len(n.requests) != 1 {
		t.Fatalf("repeat request: rel=%v notifications=%d", rel, len(n.requests))
	}

	// Rui asking Ana back meets her request: they are friends.
	rel, _, err = svc.SendRequest(ctx, rui, ana)
	if err != nil || rel != RelationFriends {
		t.Fatalf("crossing requests: rel=%v err=%v", rel, err)
	}
	if len(n.accepts) != 1 || n.accepts[0] != ana {
		t.Fatalf("original sender not told: %v", n.accepts)
	}
}

func TestSendRequestHidesBlocksAndLimits(t *testing.T) {
	ctx := context.Background()
	ana, rui, eve := uuid.New(), uuid.New(), uuid.New()
	f := newFake(ana, rui, eve)
	svc, _ := newSvc(f)

	f.blocks[[2]uuid.UUID{rui, ana}] = true // Rui blocked Ana
	if _, _, err := svc.SendRequest(ctx, ana, rui); !errors.Is(err, ErrNotFound) {
		t.Fatalf("request to someone who blocked you: err = %v, want NotFound", err)
	}

	f.sentToday = maxRequestsPerDay
	if _, _, err := svc.SendRequest(ctx, ana, eve); !errors.Is(err, ErrLimited) {
		t.Fatalf("over the daily limit: err = %v", err)
	}
}

func TestRespondAccepts(t *testing.T) {
	ctx := context.Background()
	ana, rui := uuid.New(), uuid.New()
	f := newFake(ana, rui)
	svc, n := newSvc(f)
	_, id, _ := svc.SendRequest(ctx, ana, rui)

	if _, err := svc.Respond(ctx, ana, id, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the sender answered their own request: err = %v", err)
	}
	friend, err := svc.Respond(ctx, rui, id, true)
	if err != nil || friend.GetId() != ana.String() {
		t.Fatalf("accept: friend=%v err=%v", friend, err)
	}
	if friends, _, _ := f.Relation(ctx, ana, rui); !friends {
		t.Fatal("not friends after accepting")
	}
	if len(n.accepts) != 1 || n.accepts[0] != ana {
		t.Fatalf("sender not told: %v", n.accepts)
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
	if len(n.accepts) != 1 || n.accepts[0] != ana {
		t.Fatalf("inviter not told: %v", n.accepts)
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
	f.blocks[[2]uuid.UUID{ana, rui}] = true
	if _, _, err := svc.AcceptInvite(ctx, rui, inv.Code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a blocked user accepted an invite: err = %v", err)
	}
}

func TestMatchContacts(t *testing.T) {
	ctx := context.Background()
	me, ana, rui, eve := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := newFake(me, ana, rui, eve)
	f.hashes = map[string]uuid.UUID{"h-me": me, "h-ana": ana, "h-rui": rui, "h-eve": eve}
	f.friends[[2]uuid.UUID{me, rui}] = true
	f.blocks[[2]uuid.UUID{eve, me}] = true // Eve blocked me
	svc, _ := newSvc(f)

	got, err := svc.MatchContacts(ctx, me, []string{"h-me", "h-ana", "h-ana", "h-rui", "h-eve", "h-nobody"})
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]Relation{}
	for _, c := range got {
		rels[c.Hash] = c.Relation
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
	me, friend, stranger, blocker := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := newFake(me, friend, stranger, blocker)
	f.friends[[2]uuid.UUID{friend, me}] = true
	f.blocks[[2]uuid.UUID{me, blocker}] = true // I blocked `blocker`
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
	if _, err := svc.Profile(ctx, blocker, me); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a blocked user saw the profile: err = %v", err)
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
	f.blocks[[2]uuid.UUID{blocker, me}] = true
	got, err := svc.FacebookFriends(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].User.GetId() != friend.String() {
		t.Fatalf("matches = %+v, want only the friend who did not block me", got)
	}
}
