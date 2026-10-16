package preference

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// refreshTimeout bounds one recompute. RecomputeUser is pure SQL over stored
// embeddings, so anything slower than this is a stuck database, not work.
const refreshTimeout = 30 * time.Second

// Kicker asks for a user's taste profile to be rebuilt soon. Domains that
// record explicit feedback (a rating, a visit, a dismissal) depend on this
// rather than on the reranker, so the next suggestion reflects the feedback
// instead of waiting for the 15-minute rerank pass.
type Kicker interface {
	Kick(userID uuid.UUID)
}

// Refresher debounces Kick per user: a burst of feedback within delay costs
// one recompute. It is safe to use a nil *Refresher (every call is a no-op).
type Refresher struct {
	recompute func(context.Context, uuid.UUID) error
	delay     time.Duration
	log       *slog.Logger

	mu      sync.Mutex
	pending map[uuid.UUID]*time.Timer
	closed  bool
}

// NewRefresher builds a Refresher around recompute (Reranker.RecomputeUser in
// production).
func NewRefresher(recompute func(context.Context, uuid.UUID) error, delay time.Duration, log *slog.Logger) *Refresher {
	if log == nil {
		log = slog.Default()
	}
	return &Refresher{
		recompute: recompute,
		delay:     delay,
		log:       log.With(slog.String("component", "preference-refresher")),
		pending:   map[uuid.UUID]*time.Timer{},
	}
}

// Kick schedules a recompute for userID after the debounce delay, unless one
// is already scheduled.
func (r *Refresher) Kick(userID uuid.UUID) {
	if r == nil || userID == uuid.Nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if _, ok := r.pending[userID]; ok {
		return
	}
	r.pending[userID] = time.AfterFunc(r.delay, func() { r.fire(userID) })
}

func (r *Refresher) fire(userID uuid.UUID) {
	r.mu.Lock()
	if _, ok := r.pending[userID]; !ok {
		r.mu.Unlock()
		return
	}
	delete(r.pending, userID)
	r.mu.Unlock()
	r.run(userID)
}

func (r *Refresher) run(userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	if err := r.recompute(ctx, userID); err != nil {
		r.log.Warn("taste refresh failed", slog.String("user_id", userID.String()), slog.Any("error", err))
	}
}

// Close runs every pending recompute now and ignores later kicks, so feedback
// recorded just before shutdown is not left waiting for the rerank pass.
func (r *Refresher) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.closed = true
	users := make([]uuid.UUID, 0, len(r.pending))
	for id, t := range r.pending {
		if t.Stop() {
			users = append(users, id)
		}
	}
	r.pending = map[uuid.UUID]*time.Timer{}
	r.mu.Unlock()
	for _, id := range users {
		r.run(id)
	}
}
