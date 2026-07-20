// Package resumebuf holds a small, bounded, per-session ring of recently emitted
// stream events so a client that drops mid-stream can reconnect with a
// resume_token (the last event id it saw) and replay what it missed.
//
// The chat generation goroutine survives client disconnect (context.WithoutCancel)
// and keeps appending here, so by the time the client reconnects the buffer holds
// everything produced while it was gone — including the terminal complete event
// once generation finishes.
package resumebuf

import (
	"sync"
	"time"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

const (
	defaultMaxEvents  = 500             // ring capacity per session
	defaultSessionTTL = 15 * time.Minute // idle sessions evicted after this
)

type sessionBuf struct {
	events     []locitypes.StreamEvent
	lastAccess time.Time
}

// Buffer is a concurrency-safe collection of per-session event rings.
type Buffer struct {
	mu         sync.Mutex
	sessions   map[string]*sessionBuf
	maxEvents  int
	ttl        time.Duration
	now        func() time.Time
	lastReap   time.Time
}

// New returns a Buffer with default bounds.
func New() *Buffer {
	return &Buffer{
		sessions:  make(map[string]*sessionBuf),
		maxEvents: defaultMaxEvents,
		ttl:       defaultSessionTTL,
		now:       time.Now,
	}
}

// Append records an event for a session, evicting the oldest when the ring is
// full. sessionID == "" is ignored (nothing to resume against).
func (b *Buffer) Append(sessionID string, ev locitypes.StreamEvent) {
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.reapLocked(now)

	s := b.sessions[sessionID]
	if s == nil {
		s = &sessionBuf{}
		b.sessions[sessionID] = s
	}
	s.lastAccess = now
	s.events = append(s.events, ev)
	if len(s.events) > b.maxEvents {
		// Drop the oldest, keep the most recent maxEvents.
		s.events = s.events[len(s.events)-b.maxEvents:]
	}
}

// Replay returns the buffered events for a session that were emitted after
// afterEventID. `ok` is false when the session is unknown (nothing to replay).
// If afterEventID is empty or not found in the ring, all buffered events are
// returned so the client can fully re-sync.
func (b *Buffer) Replay(sessionID, afterEventID string) (events []locitypes.StreamEvent, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := b.sessions[sessionID]
	if s == nil {
		return nil, false
	}
	s.lastAccess = b.now()

	start := 0
	if afterEventID != "" {
		for i, ev := range s.events {
			if ev.EventID == afterEventID {
				start = i + 1
				break
			}
		}
	}
	out := make([]locitypes.StreamEvent, len(s.events)-start)
	copy(out, s.events[start:])
	return out, true
}

// Drop removes a session's buffer (e.g. after a clean terminal replay).
func (b *Buffer) Drop(sessionID string) {
	b.mu.Lock()
	delete(b.sessions, sessionID)
	b.mu.Unlock()
}

// reapLocked evicts idle sessions. Called under mu, throttled to once per minute.
func (b *Buffer) reapLocked(now time.Time) {
	if now.Sub(b.lastReap) < time.Minute {
		return
	}
	b.lastReap = now
	for id, s := range b.sessions {
		if now.Sub(s.lastAccess) > b.ttl {
			delete(b.sessions, id)
		}
	}
}
