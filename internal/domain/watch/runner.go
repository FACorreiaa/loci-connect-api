package watch

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Locker elects one runner across replicas. PgLocker is the real one.
type Locker interface {
	// TryLock returns an unlock func, or ErrLockHeld without waiting.
	TryLock(ctx context.Context) (unlock func(), err error)
}

// dueRunner is the part of Service the loop drives.
type dueRunner interface {
	RunDue(ctx context.Context, limit int) (int, error)
}

// Runner wakes every Interval and, if it wins the advisory lock, runs the
// watches that are due. Every replica starts one; at most one does work in a
// given tick.
type Runner struct {
	svc      dueRunner
	locker   Locker
	logger   *slog.Logger
	interval time.Duration
	batch    int
}

// DefaultTick is how often the runner looks for due watches.
const DefaultTick = time.Minute

// defaultBatch bounds the LLM calls one tick makes; the rest wait a minute.
const defaultBatch = 20

func NewRunner(svc dueRunner, locker Locker, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{svc: svc, locker: locker, logger: logger, interval: DefaultTick, batch: defaultBatch}
}

// Run ticks until ctx is cancelled. It returns nil on cancellation.
func (r *Runner) Run(ctx context.Context) error {
	r.logger.Info("standing-task runner started", slog.Duration("tick", r.interval))
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

// Tick runs one round: take the lock or step aside, run what is due, unlock.
// It reports whether this replica did the work.
func (r *Runner) Tick(ctx context.Context) bool {
	unlock, err := r.locker.TryLock(ctx)
	if errors.Is(err, ErrLockHeld) {
		return false
	}
	if err != nil {
		r.logger.WarnContext(ctx, "standing-task runner could not take its lock", slog.Any("error", err))
		return false
	}
	defer unlock()

	posted, err := r.svc.RunDue(ctx, r.batch)
	if err != nil {
		r.logger.WarnContext(ctx, "standing-task runner tick failed", slog.Any("error", err))
	}
	if posted > 0 {
		r.logger.InfoContext(ctx, "standing tasks posted", slog.Int("count", posted))
	}
	return true
}
