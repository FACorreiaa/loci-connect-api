package resumebuf

import (
	"testing"
	"time"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func ev(id string) locitypes.StreamEvent { return locitypes.StreamEvent{EventID: id} }

func TestReplayAfterID(t *testing.T) {
	b := New()
	for _, id := range []string{"a", "b", "c", "d"} {
		b.Append("s1", ev(id))
	}
	got, ok := b.Replay("s1", "b")
	if !ok {
		t.Fatal("session should exist")
	}
	if len(got) != 2 || got[0].EventID != "c" || got[1].EventID != "d" {
		t.Fatalf("expected c,d got %+v", got)
	}
}

func TestReplayUnknownSession(t *testing.T) {
	b := New()
	if _, ok := b.Replay("nope", ""); ok {
		t.Fatal("unknown session should return ok=false")
	}
}

func TestReplayUnknownTokenReturnsAll(t *testing.T) {
	b := New()
	b.Append("s1", ev("a"))
	b.Append("s1", ev("b"))
	got, ok := b.Replay("s1", "zzz") // token not in buffer -> full re-sync
	if !ok || len(got) != 2 {
		t.Fatalf("expected all 2, got %d ok=%v", len(got), ok)
	}
}

func TestRingCap(t *testing.T) {
	b := New()
	b.maxEvents = 3
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		b.Append("s1", ev(id))
	}
	got, _ := b.Replay("s1", "")
	if len(got) != 3 || got[0].EventID != "c" || got[2].EventID != "e" {
		t.Fatalf("expected c,d,e got %+v", got)
	}
}

func TestEviction(t *testing.T) {
	b := New()
	now := time.Unix(1000, 0)
	b.now = func() time.Time { return now }
	b.Append("s1", ev("a"))

	// Advance past TTL + reap throttle, then touch a different session to trigger reap.
	now = now.Add(defaultSessionTTL + 2*time.Minute)
	b.Append("s2", ev("x"))

	if _, ok := b.Replay("s1", ""); ok {
		t.Fatal("s1 should have been evicted")
	}
	if _, ok := b.Replay("s2", ""); !ok {
		t.Fatal("s2 should still exist")
	}
}
