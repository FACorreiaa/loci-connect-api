package runs

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// FinishListener hears about a run the moment it stops running. Push
// notification is the only listener; it must not block.
type FinishListener func(ctx context.Context, run Run)

// Tracker turns one stream's events into writes on its run row. The
// handler feeds it from the live loop and, after a disconnect, from the
// drain goroutine, so the row finishes whether or not anybody is watching.
type Tracker struct {
	store    Store
	runID    uuid.UUID
	onFinish FinishListener
	logger   *slog.Logger

	mu       sync.Mutex
	attached bool
	finished bool
}

func NewTracker(store Store, runID uuid.UUID, onFinish FinishListener, logger *slog.Logger) *Tracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Tracker{store: store, runID: runID, onFinish: onFinish, logger: logger}
}

// writeCtx outlives the RPC: the row must be written after the client left.
func writeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (t *Tracker) Observe(ev locitypes.StreamEvent) {
	if t == nil {
		return
	}
	switch ev.Type {
	case locitypes.EventTypeStart:
		t.attach(ev)
	case locitypes.EventTypeComplete:
		t.finish(StatusDone, "")
	case locitypes.EventTypeError:
		if ev.StopIndex != nil {
			// One city of a multi-city trip failed; the run goes on.
			return
		}
		code := string(ev.ErrorCode)
		if code == "" {
			code = "internal"
		}
		t.finish(StatusFailed, code)
	}
}

func (t *Tracker) Close() {
	if t == nil {
		return
	}
	t.finish(StatusFailed, "incomplete")
}

func (t *Tracker) attach(ev locitypes.StreamEvent) {
	var sd locitypes.StreamStartData
	switch d := ev.Data.(type) {
	case locitypes.StreamStartData:
		sd = d
	case *locitypes.StreamStartData:
		sd = *d
	default:
		raw, _ := json.Marshal(ev.Data)
		_ = json.Unmarshal(raw, &sd)
	}
	sid, err := uuid.Parse(sd.SessionID)
	if err != nil {
		return
	}
	t.mu.Lock()
	if t.attached {
		t.mu.Unlock()
		return
	}
	t.attached = true
	t.mu.Unlock()

	ctx, cancel := writeCtx()
	defer cancel()
	if err := t.store.Attach(ctx, t.runID, sid, sd.Domain, sd.City); err != nil {
		t.logger.Warn("attach run", "run_id", t.runID, "error", err)
	}
}

func (t *Tracker) finish(status Status, code string) {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	t.finished = true
	t.mu.Unlock()

	ctx, cancel := writeCtx()
	defer cancel()
	run, moved, err := t.store.Finish(ctx, t.runID, status, code)
	if err != nil {
		t.logger.Warn("finish run", "run_id", t.runID, "status", status, "error", err)
		return
	}
	// A run that ended before its start event never learned its session, so
	// there is nothing to link a notification to: record the failure, but
	// announce nothing.
	t.mu.Lock()
	attached := t.attached
	t.mu.Unlock()
	if moved && attached && t.onFinish != nil {
		t.onFinish(context.WithoutCancel(ctx), run)
	}
}
