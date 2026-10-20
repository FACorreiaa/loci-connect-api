package ranking

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
)

func TestTasteSignal(t *testing.T) {
	cases := []struct {
		v      Verdict
		event  string
		weight float32
	}{
		{Loved, preference.EventVisited, 1.0},
		{Fine, preference.EventVisited, 0.2},
		{NotForMe, preference.EventSkipped, 1.5},
	}
	for _, c := range cases {
		event, weight := TasteSignal(c.v)
		if event != c.event || weight != c.weight {
			t.Errorf("TasteSignal(%d) = %q, %v; want %q, %v", c.v, event, weight, c.event, c.weight)
		}
	}
}

func TestScore(t *testing.T) {
	cases := []struct {
		v           Verdict
		index, size int
		want        float32
	}{
		{Loved, 0, 1, 10},
		{Loved, 2, 3, 6.7},
		{Fine, 0, 2, 6.6},
		{Fine, 1, 2, 3.4},
		{NotForMe, 0, 2, 3.3},
		{NotForMe, 1, 2, 0},
	}
	for _, c := range cases {
		if got := Score(c.v, c.index, c.size); math.Abs(float64(got-c.want)) > 1e-4 {
			t.Errorf("Score(%d, %d, %d) = %v; want %v", c.v, c.index, c.size, got, c.want)
		}
	}
}

func TestNextProbe(t *testing.T) {
	if mid, done := nextProbe(0, 4); done || mid != 2 {
		t.Fatalf("nextProbe(0,4) = %d, %v", mid, done)
	}
	if _, done := nextProbe(3, 3); !done {
		t.Fatal("nextProbe(3,3) should be done")
	}
}

func TestNarrow(t *testing.T) {
	cases := []struct {
		lo, hi, idx    int
		better         bool
		wantLo, wantHi int
	}{
		{0, 4, 2, true, 0, 2},
		{0, 4, 2, false, 3, 4},
		{2, 4, 0, true, 2, 2},  // answer about a place above the range: clamps, places at lo
		{0, 2, 3, false, 2, 2}, // below the range
		{0, 4, -1, true, 0, 4}, // stale: not in the bucket, nothing learned
	}
	for _, c := range cases {
		lo, hi := narrow(c.lo, c.hi, c.idx, c.better)
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("narrow(%d,%d,%d,%v) = %d,%d; want %d,%d", c.lo, c.hi, c.idx, c.better, lo, hi, c.wantLo, c.wantHi)
		}
	}
}

type fakeFriends struct {
	friends, blocked bool
	calls            int
}

func (f *fakeFriends) Relation(context.Context, uuid.UUID, uuid.UUID) (bool, bool, error) {
	f.calls++
	return f.friends, f.blocked, nil
}

type fakeRepo struct {
	Repository
	listed  []Place
	rated   *Place
	deleted bool
}

func (r *fakeRepo) List(context.Context, uuid.UUID, string, int) ([]Place, error) {
	return r.listed, nil
}

func (r *fakeRepo) Rate(_ context.Context, _ uuid.UUID, in RateInput) (Place, *Comparison, error) {
	p := Place{POIID: in.POIID, Verdict: in.Verdict}
	r.rated = &p
	return p, nil, nil
}

func (r *fakeRepo) Delete(context.Context, uuid.UUID, string) (bool, error) {
	return r.deleted, nil
}

type tasteCall struct {
	op, key, value, event string
	opts                  preference.RecordOpts
}

type fakeTaste struct {
	calls []tasteCall
	kicks int
}

func (f *fakeTaste) Record(context.Context, uuid.UUID, string, preference.RecordOpts) {}

func (f *fakeTaste) Replace(_ context.Context, _ uuid.UUID, key, value, event string, opts preference.RecordOpts) {
	f.calls = append(f.calls, tasteCall{op: "replace", key: key, value: value, event: event, opts: opts})
}

func (f *fakeTaste) Forget(_ context.Context, _ uuid.UUID, key, value string) {
	f.calls = append(f.calls, tasteCall{op: "forget", key: key, value: value})
}

func (f *fakeTaste) Kick(uuid.UUID) { f.kicks++ }

func TestListFor_FriendsOnly(t *testing.T) {
	ctx := context.Background()
	viewer, owner := uuid.New(), uuid.New()
	repo := &fakeRepo{listed: []Place{{POIID: "a"}}}

	stranger := &fakeFriends{}
	if _, err := NewService(repo, stranger, nil, nil, nil).ListFor(ctx, viewer, owner, "", 0); !errors.Is(err, ErrNotFriends) {
		t.Fatalf("stranger: err = %v, want ErrNotFriends", err)
	}
	blocked := &fakeFriends{friends: true, blocked: true}
	if _, err := NewService(repo, blocked, nil, nil, nil).ListFor(ctx, viewer, owner, "", 0); !errors.Is(err, ErrNotFriends) {
		t.Fatalf("blocked: err = %v, want ErrNotFriends", err)
	}
	friend := &fakeFriends{friends: true}
	got, err := NewService(repo, friend, nil, nil, nil).ListFor(ctx, viewer, owner, "", 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("friend: got %v, %v", got, err)
	}
	self := &fakeFriends{}
	if _, err := NewService(repo, self, nil, nil, nil).ListFor(ctx, owner, owner, "", 0); err != nil || self.calls != 0 {
		t.Fatalf("self: err = %v, relation calls = %d; want nil, 0", err, self.calls)
	}
}

func TestRatePlace_RecordsTasteAndKicks(t *testing.T) {
	taste := &fakeTaste{}
	svc := NewService(&fakeRepo{}, nil, taste, taste, nil)
	if _, _, err := svc.RatePlace(context.Background(), uuid.New(), RateInput{POIID: "poi-1", Verdict: NotForMe, CityName: "Lisbon", POIName: "X"}); err != nil {
		t.Fatal(err)
	}
	if len(taste.calls) != 1 {
		t.Fatalf("taste calls = %+v", taste.calls)
	}
	c := taste.calls[0]
	if c.op != "replace" || c.key != "ranking_poi" || c.value != "poi-1" || c.event != preference.EventSkipped ||
		c.opts.Weight != 1.5 || c.opts.POIID != "poi-1" || c.opts.Metadata["ranking_poi"] != "poi-1" {
		t.Fatalf("taste call = %+v", c)
	}
	if taste.kicks != 1 {
		t.Fatalf("kicks = %d, want 1", taste.kicks)
	}
}

func TestDelete_ForgetsOnlyWhenSomethingWasDeleted(t *testing.T) {
	taste := &fakeTaste{}
	svc := NewService(&fakeRepo{deleted: false}, nil, taste, taste, nil)
	if _, err := svc.Delete(context.Background(), uuid.New(), "poi-1"); err != nil {
		t.Fatal(err)
	}
	if len(taste.calls) != 0 || taste.kicks != 0 {
		t.Fatalf("nothing deleted, but taste = %+v kicks %d", taste.calls, taste.kicks)
	}
	svc = NewService(&fakeRepo{deleted: true}, nil, taste, taste, nil)
	if _, err := svc.Delete(context.Background(), uuid.New(), "poi-1"); err != nil {
		t.Fatal(err)
	}
	if len(taste.calls) != 1 || taste.calls[0].op != "forget" || taste.calls[0].value != "poi-1" || taste.kicks != 1 {
		t.Fatalf("taste = %+v kicks %d", taste.calls, taste.kicks)
	}
}
