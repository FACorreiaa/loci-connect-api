package concurrency

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestLLMSemaphore_NilIsNoOp(t *testing.T) {
	release, err := (*LLMSemaphore)(nil).Acquire(context.Background())
	if err != nil {
		t.Fatalf("nil semaphore acquire: %v", err)
	}
	release()
}

func TestLLMSemaphore_LimitsConcurrency(t *testing.T) {
	sem := NewLLMSemaphore(1)
	ctx := context.Background()

	release1, err := sem.Acquire(ctx)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, err := sem.Acquire(ctx)
		if err != nil {
			t.Errorf("second acquire: %v", err)
			return
		}
		close(acquired)
		release2()
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire should block while first slot is held")
	case <-time.After(50 * time.Millisecond):
	}

	release1()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second acquire should succeed after release")
	}
}

func TestLLMSemaphore_RespectsContextCancel(t *testing.T) {
	sem := NewLLMSemaphore(1)
	release, err := sem.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := sem.Acquire(ctx); err == nil {
		t.Fatal("expected acquire to fail when context is canceled")
	}
}

func TestNewLLMSemaphore_ZeroIsNil(t *testing.T) {
	if NewLLMSemaphore(0) != nil {
		t.Fatal("expected nil semaphore for maxConcurrent <= 0")
	}
}

func TestLLMSemaphore_AllowsMaxConcurrent(t *testing.T) {
	const max = 3
	sem := NewLLMSemaphore(max)
	ctx := context.Background()

	releases := make([]func(), 0, max)
	for i := 0; i < max; i++ {
		release, err := sem.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	var blocked atomic.Bool
	done := make(chan struct{})
	go func() {
		release, err := sem.Acquire(ctx)
		if err != nil {
			t.Errorf("overflow acquire: %v", err)
			return
		}
		blocked.Store(true)
		release()
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	if blocked.Load() {
		t.Fatal("fourth acquire should block at capacity")
	}

	for _, release := range releases {
		release()
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fourth acquire should proceed after releases")
	}
}
