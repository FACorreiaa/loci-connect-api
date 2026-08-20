package concurrency

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestGo_NonPanickingFRunsAndWaitReturns(t *testing.T) {
	var wg sync.WaitGroup
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	var ran atomic.Bool
	Go(&wg, logger, func() {
		ran.Store(true)
	})

	wg.Wait()

	if !ran.Load() {
		t.Fatal("expected f to run")
	}
}

func TestGo_PanickingFIsContainedAndLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	var wg sync.WaitGroup
	Go(&wg, logger, func() {
		panic("boom")
	})

	// If panic recovery fails, this Wait would block forever or the process crashes.
	wg.Wait()

	out := buf.String()
	if !strings.Contains(out, "worker goroutine panicked") {
		t.Fatalf("expected panic log, got: %q", out)
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("expected panic value in log, got: %q", out)
	}
}

func TestGo_NilLoggerDoesNotCrashOnPanic(_ *testing.T) {
	var wg sync.WaitGroup
	Go(&wg, nil, func() {
		panic("nil-logger-panic")
	})
	wg.Wait()
}

func TestGo_IncrementsAndDecrementsWaitGroup(t *testing.T) {
	var wg sync.WaitGroup
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	const n = 10
	var counter atomic.Int32
	for i := 0; i < n; i++ {
		Go(&wg, logger, func() {
			counter.Add(1)
		})
	}

	wg.Wait()

	if got := counter.Load(); got != n {
		t.Fatalf("expected counter %d, got %d", n, got)
	}
}

func TestGo_MixedPanicAndSuccessAllDone(t *testing.T) {
	var wg sync.WaitGroup
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	var ok atomic.Int32
	for i := 0; i < 5; i++ {
		Go(&wg, logger, func() {
			ok.Add(1)
		})
		Go(&wg, logger, func() {
			panic("partial")
		})
	}

	// Verifies wg.Wait returns even with panics interleaved.
	wg.Wait()

	if ok.Load() != 5 {
		t.Fatalf("expected 5 successful runs, got %d", ok.Load())
	}
}
