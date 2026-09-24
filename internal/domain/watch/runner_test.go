package watch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// memLocker is an in-process stand-in for the advisory lock: one holder at a
// time, TryLock never waits.
type memLocker struct {
	mu      sync.Mutex
	held    bool
	err     error
	unlocks int
}

func (l *memLocker) TryLock(context.Context) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	if l.held {
		return nil, ErrLockHeld
	}
	l.held = true
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.held = false
		l.unlocks++
	}, nil
}

type countingRunner struct {
	calls atomic.Int32
	block chan struct{} // when set, RunDue waits on it
	err   error
}

func (c *countingRunner) RunDue(ctx context.Context, _ int) (int, error) {
	c.calls.Add(1)
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
		}
	}
	return 1, c.err
}

func TestTickRunsDueAndReleasesLock(t *testing.T) {
	lock := &memLocker{}
	svc := &countingRunner{}
	r := NewRunner(svc, lock, nil)

	require.True(t, r.Tick(context.Background()))
	require.EqualValues(t, 1, svc.calls.Load())
	require.Equal(t, 1, lock.unlocks)
	require.False(t, lock.held)
}

func TestTickStepsAsideWhenAnotherReplicaHoldsTheLock(t *testing.T) {
	lock := &memLocker{held: true}
	svc := &countingRunner{}
	r := NewRunner(svc, lock, nil)

	require.False(t, r.Tick(context.Background()))
	require.Zero(t, svc.calls.Load())
}

func TestTickStepsAsideWhenLockErrors(t *testing.T) {
	lock := &memLocker{err: errors.New("db unreachable")}
	svc := &countingRunner{}
	require.False(t, NewRunner(svc, lock, nil).Tick(context.Background()))
	require.Zero(t, svc.calls.Load())
}

func TestTickReleasesLockEvenWhenRunFails(t *testing.T) {
	lock := &memLocker{}
	svc := &countingRunner{err: errors.New("claim failed")}
	require.True(t, NewRunner(svc, lock, nil).Tick(context.Background()))
	require.False(t, lock.held)
}

// Two replicas sharing one lock: while the first is mid-run, the second's
// tick does nothing.
func TestConcurrentReplicasOnlyOneRuns(t *testing.T) {
	lock := &memLocker{}
	svc := &countingRunner{block: make(chan struct{})}
	a, b := NewRunner(svc, lock, nil), NewRunner(svc, lock, nil)

	done := make(chan bool)
	go func() { done <- a.Tick(context.Background()) }()
	require.Eventually(t, func() bool { return svc.calls.Load() == 1 }, time.Second, time.Millisecond)

	require.False(t, b.Tick(context.Background()))
	close(svc.block)
	require.True(t, <-done)
	require.EqualValues(t, 1, svc.calls.Load())
}

func TestRunTicksUntilCancelled(t *testing.T) {
	svc := &countingRunner{}
	r := NewRunner(svc, &memLocker{}, nil)
	r.interval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() { errc <- r.Run(ctx) }()
	require.Eventually(t, func() bool { return svc.calls.Load() >= 2 }, time.Second, time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
