package recap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/pglock"
)

// Locker elects one runner across replicas (pglock.Locker).
type Locker interface {
	TryLock(ctx context.Context) (unlock func(), err error)
}

// RunnerTick is how often the runner wakes. A recap a few minutes late does
// not matter; a push at 03:00 would, and InPushWindow handles that.
const RunnerTick = 10 * time.Minute

// Runner queues, builds and announces recaps in the background. Every replica
// starts one; the advisory lock lets one of them work in any tick. It runs
// in the API process because the push senders live there.
type Runner struct {
	svc      *Service
	locker   Locker
	log      *slog.Logger
	interval time.Duration
}

// NewRunner builds the runner.
func NewRunner(svc *Service, locker Locker, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{svc: svc, locker: locker, log: log.With(slog.String("component", "recap-runner")), interval: RunnerTick}
}

// Run ticks until ctx is cancelled. It returns nil on cancellation.
func (r *Runner) Run(ctx context.Context) error {
	r.log.Info("recap runner started", slog.Duration("tick", r.interval))
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.Tick(ctx)
		}
	}
}

// Tick runs one round under the lock. It reports whether this replica did
// the work.
func (r *Runner) Tick(ctx context.Context) bool {
	unlock, err := r.locker.TryLock(ctx)
	if errors.Is(err, pglock.ErrHeld) {
		return false
	}
	if err != nil {
		r.log.WarnContext(ctx, "recap runner could not take its lock", slog.Any("error", err))
		return false
	}
	defer unlock()

	res, err := r.svc.Tick(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "recap tick failed", slog.Any("error", err))
	}
	if res != (TickResult{}) {
		r.log.InfoContext(ctx, "recap tick",
			slog.Int("queued", res.Queued), slog.Int("built", res.Built),
			slog.Int("ineligible", res.Ineligible), slog.Int("failed", res.Failed),
			slog.Int("notified", res.Notified))
	}
	return true
}
