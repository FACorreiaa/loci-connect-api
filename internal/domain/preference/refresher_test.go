package preference

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type countingRecompute struct {
	mu    sync.Mutex
	calls map[uuid.UUID]int
}

func (c *countingRecompute) fn(_ context.Context, id uuid.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[uuid.UUID]int{}
	}
	c.calls[id]++
	return nil
}

func (c *countingRecompute) count(id uuid.UUID) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[id]
}

func TestRefresher_DebouncesPerUser(t *testing.T) {
	rc := &countingRecompute{}
	r := NewRefresher(rc.fn, 20*time.Millisecond, nil)
	defer r.Close()

	a, b := uuid.New(), uuid.New()
	r.Kick(a)
	r.Kick(a)
	r.Kick(a)
	r.Kick(b)

	time.Sleep(80 * time.Millisecond)
	if got := rc.count(a); got != 1 {
		t.Fatalf("user a recomputed %d times, want 1", got)
	}
	if got := rc.count(b); got != 1 {
		t.Fatalf("user b recomputed %d times, want 1", got)
	}

	// A kick after the burst has run schedules a fresh recompute.
	r.Kick(a)
	time.Sleep(80 * time.Millisecond)
	if got := rc.count(a); got != 2 {
		t.Fatalf("user a recomputed %d times after second burst, want 2", got)
	}
}

func TestRefresher_CloseFlushesPending(t *testing.T) {
	rc := &countingRecompute{}
	r := NewRefresher(rc.fn, time.Hour, nil)
	id := uuid.New()
	r.Kick(id)
	r.Close()
	if got := rc.count(id); got != 1 {
		t.Fatalf("pending kick recomputed %d times on Close, want 1", got)
	}
	// Kicks after Close are ignored rather than leaking timers.
	r.Kick(id)
	if got := rc.count(id); got != 1 {
		t.Fatalf("kick after Close recomputed, count %d", got)
	}
}

func TestRefresher_NilIsNoop(t *testing.T) {
	var r *Refresher
	r.Kick(uuid.New())
	r.Close()
}
