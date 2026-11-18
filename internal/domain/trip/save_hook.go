package trip

import (
	"context"
	"sync/atomic"
	"time"
)

// SaveNotifier is a Repository that tells one listener about every trip it
// saves. It exists so something built later than the repository (the stop
// checker) can hear about trips the chat pipeline saves, without the chat
// pipeline knowing it is there.
type SaveNotifier struct {
	Repository
	listener atomic.Pointer[func(*Trip)]
}

// NotifyOnSave wraps repo. Until OnSave is called it behaves exactly as repo.
func NotifyOnSave(repo Repository) *SaveNotifier {
	return &SaveNotifier{Repository: repo}
}

// OnSave sets the listener, replacing any before it. The listener is called
// on its own goroutine after a save succeeds, with the trip as stored, and
// must not hold the trip beyond the call.
func (n *SaveNotifier) OnSave(fn func(*Trip)) { n.listener.Store(&fn) }

// SaveTrip saves through to the wrapped repository and then tells the
// listener. The save's result never depends on the listener.
func (n *SaveNotifier) SaveTrip(ctx context.Context, t *Trip, baseVersion int64) (*Trip, error) {
	saved, err := n.Repository.SaveTrip(ctx, t, baseVersion)
	if err != nil || saved == nil {
		return saved, err
	}
	if fn := n.listener.Load(); fn != nil {
		go (*fn)(saved)
	}
	return saved, nil
}

// warmChecksTimeout bounds the place lookups one saved trip may cost before
// the map is even asked.
const warmChecksTimeout = 20 * time.Second

// WarmStopChecks starts the map lookups for a trip's places, so the checks
// are there by the time someone opens it. Without this the first read of a
// new trip shows no stamps: a read never waits for the map, it only sets the
// lookups off.
//
// It is the read path run for its side effect, so what gets looked up is
// exactly what a read would ask for. Safe to call for any trip, any number of
// times: places already checked are not asked about again.
func (h *Handler) WarmStopChecks(t *Trip) {
	if h.checks == nil || t == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), warmChecksTimeout)
	defer cancel()
	draft := tripToProto(t)
	h.hydrateStops(ctx, draft)
	h.checkStops(ctx, draft)
}
