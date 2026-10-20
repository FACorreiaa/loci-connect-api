package journal

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/pglock"
	"github.com/FACorreiaa/loci-connect-api/pkg/storage"
)

// SweeperLockKey keeps one replica sweeping at a time.
const SweeperLockKey int64 = 0x6c6f63696a726e // "locijrn"

const (
	sweepEvery = 10 * time.Minute
	sweepBatch = 200
	// tombstoneGrace outlives every signed PUT URL: deleting a key while its
	// URL is still valid would let a late upload recreate it with no row and
	// no tombstone left to find it.
	tombstoneGrace = putTTL + 5*time.Minute
)

// Locker is the advisory lock the sweeper runs under (pglock.Locker).
type Locker interface {
	TryLock(ctx context.Context) (unlock func(), err error)
}

// Sweeper removes uploads never confirmed within a day, and deletes the
// objects of photo rows that are gone (tombstoned by the 0123 trigger).
type Sweeper struct {
	repo   Repository
	signer storage.Signer
	lock   Locker
	log    *slog.Logger
}

// NewSweeper builds the sweeper. lock may be nil (tests); signer may be nil
// (storage off), in which case tombstones wait.
func NewSweeper(repo Repository, signer storage.Signer, lock Locker, log *slog.Logger) *Sweeper {
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{repo: repo, signer: signer, lock: lock, log: log.With(slog.String("component", "journal-sweeper"))}
}

// Run sweeps every ten minutes until ctx ends.
func (s *Sweeper) Run(ctx context.Context) error {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (s *Sweeper) tick(ctx context.Context) {
	if s.lock != nil {
		unlock, err := s.lock.TryLock(ctx)
		if err != nil {
			if !errors.Is(err, pglock.ErrHeld) && ctx.Err() == nil {
				s.log.WarnContext(ctx, "journal sweep lock", slog.Any("error", err))
			}
			return
		}
		defer unlock()
	}
	if err := s.Sweep(ctx, time.Now()); err != nil && ctx.Err() == nil {
		s.log.WarnContext(ctx, "journal sweep failed", slog.Any("error", err))
	}
}

// Sweep is one pass.
func (s *Sweeper) Sweep(ctx context.Context, now time.Time) error {
	if n, err := s.repo.SweepPending(ctx, now.Add(-pendingTTL)); err != nil {
		return err
	} else if n > 0 {
		s.log.InfoContext(ctx, "swept unconfirmed photo uploads", slog.Int64("count", n))
	}
	if s.signer == nil {
		return nil
	}
	keys, err := s.repo.Tombstones(ctx, now.Add(-tombstoneGrace), sweepBatch)
	if err != nil {
		return err
	}
	done := make([]string, 0, len(keys))
	for _, k := range keys {
		if err := s.signer.Delete(ctx, k); err != nil {
			s.log.WarnContext(ctx, "delete photo object", slog.String("key", k), slog.Any("error", err))
			continue
		}
		done = append(done, k)
	}
	return s.repo.ClearTombstones(ctx, done)
}
