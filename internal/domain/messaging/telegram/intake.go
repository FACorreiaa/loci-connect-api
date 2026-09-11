package telegram

import (
	"sync"

	"golang.org/x/time/rate"
)

// seenCapacity is how many update ids are remembered.
//
// Telegram retries over seconds to minutes, so the window that matters is
// short; this is far more than fits in it and still a fixed, small amount of
// memory.
const seenCapacity = 4096

// seen remembers update ids this process has already taken on.
//
// Telegram redelivers anything it does not get a prompt 200 for, and the
// webhook answers long after it has replied — so a redelivery is ordinary
// rather than exceptional. Answering one twice used to cost a duplicate
// message, which was tolerable. It now costs a second download, a second
// transcription, a second generation and a second voice note in the chat,
// which reads as a broken bot rather than a wasteful one.
//
// In memory, and bounded, because the deployment runs one replica — a
// constraint that already exists, since migrations run on boot. A restart
// loses the set, which costs at most one duplicated answer. A second replica
// would need this in Postgres; messaging_cursors is the shape to copy.
type seen struct {
	mu    sync.Mutex
	ids   map[int64]struct{}
	order []int64
	next  int
}

func newSeen() *seen {
	return &seen{ids: make(map[int64]struct{}, seenCapacity), order: make([]int64, seenCapacity)}
}

// first reports whether this update has not been handled before, and records
// it if so.
func (s *seen) first(id int64) bool {
	if s == nil || id == 0 {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.ids[id]; ok {
		return false
	}

	// A ring rather than a growing map: the oldest id is evicted as the newest
	// arrives, so the memory is fixed whatever the traffic.
	if evicted := s.order[s.next]; evicted != 0 {
		delete(s.ids, evicted)
	}
	s.order[s.next] = id
	s.next = (s.next + 1) % len(s.order)
	s.ids[id] = struct{}{}
	return true
}

// chatLimiterEntries caps how many chats are remembered at once.
//
// Without a cap this is a map keyed by anything a stranger can invent, which
// is a way to run the pod out of memory from the outside.
const chatLimiterEntries = 1024

// chatLimiter rations what one chat can ask for.
//
// The daily quota is a counter and says nothing about a burst: an account with
// requests left can still send twenty recordings in ten seconds, and each one
// is a download, a transcription and a generation. This is what stops that,
// and it is per chat rather than global so one sender cannot starve everybody
// else.
type chatLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	every    rate.Limit
	burst    int
}

func newChatLimiter(every rate.Limit, burst int) *chatLimiter {
	if every <= 0 || burst <= 0 {
		return nil
	}
	return &chatLimiter{limiters: map[string]*rate.Limiter{}, every: every, burst: burst}
}

// allow reports whether this chat may be answered now.
func (l *chatLimiter) allow(chatID string) bool {
	if l == nil {
		return true
	}

	l.mu.Lock()
	limiter, ok := l.limiters[chatID]
	if !ok {
		// Cleared wholesale rather than evicted one at a time. The limiters
		// are identical and cheap to rebuild, and the alternative is an LRU
		// for a map that a single bot's chats will rarely fill.
		if len(l.limiters) >= chatLimiterEntries {
			l.limiters = map[string]*rate.Limiter{}
		}
		limiter = rate.NewLimiter(l.every, l.burst)
		l.limiters[chatID] = limiter
	}
	l.mu.Unlock()

	return limiter.Allow()
}
