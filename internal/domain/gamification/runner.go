package gamification

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/pglock"
)

// RunnerLockKey is the advisory lock the field runner takes; it spells
// "locifild" in ASCII.
const RunnerLockKey int64 = 0x6c6f636966696c64

// Locker elects one runner across replicas (pglock.Locker).
type Locker interface {
	TryLock(ctx context.Context) (unlock func(), err error)
}

// Runner does the field score's background work every Interval: closes
// finished seasons, pays places kept a week, and looks up neighborhoods the
// request path missed. Every replica starts one; the advisory lock lets one
// of them work in any tick.
type Runner struct {
	svc      *Service
	locker   Locker
	log      *slog.Logger
	interval time.Duration
}

// RunnerTick is how often the runner wakes.
const RunnerTick = 5 * time.Minute

// Per-tick bounds: kept awards are cheap; neighborhood lookups go to a
// third-party API, one a second.
const (
	keepBatch          = 500
	neighborhoodBatch  = 50
	neighborhoodPacing = time.Second
)

// NewRunner builds the runner.
func NewRunner(svc *Service, locker Locker, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{svc: svc, locker: locker, log: log.With(slog.String("component", "field-runner")), interval: RunnerTick}
}

// Run ticks until ctx is cancelled. It returns nil on cancellation.
func (r *Runner) Run(ctx context.Context) error {
	r.log.Info("field runner started", slog.Duration("tick", r.interval))
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

// Tick runs one round: take the lock or step aside, do the work, unlock. It
// reports whether this replica did the work.
func (r *Runner) Tick(ctx context.Context) bool {
	unlock, err := r.locker.TryLock(ctx)
	if errors.Is(err, pglock.ErrHeld) {
		return false
	}
	if err != nil {
		r.log.WarnContext(ctx, "field runner could not take its lock", slog.Any("error", err))
		return false
	}
	defer unlock()

	if n, err := r.svc.CloseSeasons(ctx); err != nil {
		r.log.WarnContext(ctx, "closing seasons failed", slog.Any("error", err))
	} else if n > 0 {
		r.log.InfoContext(ctx, "seasons closed", slog.Int("count", n))
	}
	if n, err := r.svc.PayKeeps(ctx, keepBatch); err != nil {
		r.log.WarnContext(ctx, "paying kept places failed", slog.Any("error", err))
	} else if n > 0 {
		r.log.InfoContext(ctx, "kept places paid", slog.Int("count", n))
	}
	if n, err := r.svc.BackfillNeighborhoods(ctx, neighborhoodBatch, neighborhoodPacing); err != nil {
		r.log.WarnContext(ctx, "neighborhood backfill failed", slog.Any("error", err))
	} else if n > 0 {
		r.log.InfoContext(ctx, "neighborhoods looked up", slog.Int("count", n))
	}
	return true
}
