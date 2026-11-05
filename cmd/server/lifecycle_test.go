package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Registers the loop metrics these tests read back.
	_ "github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// lockedBuffer lets a test read logs that a loop goroutine writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// metricValue reads one series from the default registry, 0 if it is absent.
func metricValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
	series:
		for _, m := range family.GetMetric() {
			for _, pair := range m.GetLabel() {
				if want, ok := labels[pair.GetName()]; ok && want != pair.GetValue() {
					continue series
				}
			}
			return m.GetCounter().GetValue() + m.GetGauge().GetValue()
		}
	}
	return 0
}

func loopStops(t *testing.T, loop, reason string) float64 {
	t.Helper()
	return metricValue(t, "loci_background_loop_stops_total", map[string]string{"loop": loop, "reason": reason})
}

func loopUp(t *testing.T, loop string) float64 {
	t.Helper()
	return metricValue(t, "loci_background_loop_up", map[string]string{"loop": loop})
}

func startServer(t *testing.T, handler http.Handler, timeout time.Duration) (url string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errs := make(chan error, 1)
	go func() {
		errs <- serve(ctx, &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}, lis, timeout, discard)
	}()
	return "http://" + lis.Addr().String(), cancel, errs
}

// An open stream outliving the drain is normal, not a failure. Reporting it as
// one is what made main exit without running its cleanup.
func TestServe_StreamOutlivingTheDrainIsNotAnError(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(released)
	})
	url, cancel, done := startServer(t, handler, 100*time.Millisecond)

	resp, err := http.Get(url) //nolint:noctx // ends when the server closes it
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	<-started

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return")
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream's request context was not cancelled")
	}
}

func TestServe_FinishesARequestInFlight(t *testing.T) {
	started := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusTeapot)
	})
	url, cancel, done := startServer(t, handler, 5*time.Second)

	got := make(chan int, 1)
	go func() {
		resp, err := http.Get(url) //nolint:noctx // bounded by the handler
		if err != nil {
			got <- 0
			return
		}
		_ = resp.Body.Close()
		got <- resp.StatusCode
	}()
	<-started
	cancel()

	assert.Equal(t, http.StatusTeapot, <-got)
	require.NoError(t, <-done)
}

func TestServe_ReportsAServerThatCannotRun(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, lis.Close())

	err = serve(context.Background(), &http.Server{ReadHeaderTimeout: time.Second}, lis, time.Second, discard)

	require.Error(t, err)
}

// A panic used to end the loop until the next deploy, with one log line and no
// stack, so nothing showed which code had failed or that the loop was gone.
func TestLoops_PanicRestartsTheLoopAndLogsTheStack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs lockedBuffer
		l := newLoops(slog.New(slog.NewTextHandler(&logs, nil)))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		before := loopStops(t, "t-panic", "panic")

		var calls atomic.Int64
		ran := make(chan struct{})
		l.start(ctx, "t-panic", func(ctx context.Context) error {
			if calls.Add(1) == 1 {
				panic("boom")
			}
			close(ran)
			<-ctx.Done()
			return nil
		})
		<-ran

		assert.Equal(t, int64(2), calls.Load())
		assert.Contains(t, logs.String(), "boom")
		assert.Contains(t, logs.String(), "lifecycle_test.go", "the log needs the stack")
		assert.InDelta(t, before+1, loopStops(t, "t-panic", "panic"), 0)
		assert.InDelta(t, 1, loopUp(t, "t-panic"), 0)

		cancel()
		require.True(t, l.wait(time.Second))
		assert.InDelta(t, 0, loopUp(t, "t-panic"), 0)
	})
}

func TestLoops_RepeatedPanicsBackOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLoops(discard)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		start := time.Now()
		var at []time.Duration
		var mu sync.Mutex
		l.start(ctx, "t-backoff", func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			at = append(at, time.Since(start))
			panic("always")
		})

		time.Sleep(8 * time.Second)
		cancel()
		require.True(t, l.wait(time.Second))

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []time.Duration{0, time.Second, 3 * time.Second, 7 * time.Second}, at)
	})
}

func TestLoops_BackoffIsCapped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLoops(discard)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		start := time.Now()
		var at []time.Duration
		var mu sync.Mutex
		l.start(ctx, "t-cap", func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			at = append(at, time.Since(start))
			panic("always")
		})

		time.Sleep(4 * time.Minute)
		cancel()
		require.True(t, l.wait(time.Second))

		mu.Lock()
		defer mu.Unlock()
		require.Greater(t, len(at), 8)
		assert.Equal(t, 32*time.Second, at[6]-at[5])
		assert.Equal(t, loopRestartMax, at[7]-at[6])
		assert.Equal(t, loopRestartMax, at[8]-at[7])
	})
}

// A panic a week apart is not a crash loop, so it should not wait as if it were.
func TestLoops_HealthyRunResetsTheBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLoops(discard)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		start := time.Now()
		var at []time.Duration
		var mu sync.Mutex
		l.start(ctx, "t-reset", func(context.Context) error {
			mu.Lock()
			at = append(at, time.Since(start))
			calls := len(at)
			mu.Unlock()
			if calls == 3 {
				time.Sleep(loopRestartMax)
			}
			panic("always")
		})

		time.Sleep(3*time.Second + loopRestartMax + 2*time.Second)
		cancel()
		require.True(t, l.wait(time.Second))

		mu.Lock()
		defer mu.Unlock()
		require.Greater(t, len(at), 3)
		assert.Equal(t, 3*time.Second+loopRestartMax+time.Second, at[3], "one base delay after the healthy run")
	})
}

// Shutdown must not sit out a backoff, or a deploy waits for the longest delay.
func TestLoops_CancelEndsABackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLoops(discard)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		l.start(ctx, "t-cancel", func(context.Context) error { panic("always") })
		synctest.Wait()

		start := time.Now()
		cancel()
		require.True(t, l.wait(time.Minute))
		assert.Zero(t, time.Since(start))
	})
}

// An error is the loop's own decision to stop, such as a rejected bot token. It
// is not retried, but it must show up, and it must not take the API down.
func TestLoops_ErrorStopsTheLoopAndIsCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs lockedBuffer
		l := newLoops(slog.New(slog.NewTextHandler(&logs, nil)))
		before := loopStops(t, "t-error", "error")

		var calls atomic.Int64
		l.start(t.Context(), "t-error", func(context.Context) error {
			calls.Add(1)
			return errors.New("rejected token")
		})
		require.True(t, l.wait(time.Hour))

		assert.Equal(t, int64(1), calls.Load())
		assert.Contains(t, logs.String(), "rejected token")
		assert.True(t, strings.Contains(logs.String(), "t-error"), "the log must name the loop")
		assert.InDelta(t, before+1, loopStops(t, "t-error", "error"), 0)
		assert.InDelta(t, 0, loopUp(t, "t-error"), 0)
	})
}

// Returning nil means switched off by configuration, which is not an incident.
func TestLoops_NilReturnIsNotCountedOrRestarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLoops(discard)
		var calls atomic.Int64
		l.start(t.Context(), "t-off", func(context.Context) error {
			calls.Add(1)
			return nil
		})
		require.True(t, l.wait(time.Hour))

		assert.Equal(t, int64(1), calls.Load())
		assert.Zero(t, loopStops(t, "t-off", "error")+loopStops(t, "t-off", "panic"))
	})
}

// A loop returning an error because it was cancelled is a normal shutdown.
func TestLoops_ErrorDuringShutdownIsNotCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLoops(discard)
		ctx, cancel := context.WithCancel(t.Context())
		l.start(ctx, "t-shutdown", func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		synctest.Wait()
		cancel()
		require.True(t, l.wait(time.Hour))

		assert.Zero(t, loopStops(t, "t-shutdown", "error")+loopStops(t, "t-shutdown", "panic"))
	})
}

// A loop stuck past the bound must not hold the process until Kubernetes kills
// it, which would lose the cleanup that follows.
func TestLoops_WaitGivesUpOnAStuckLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLoops(discard)
		release := make(chan struct{})
		l.start(t.Context(), "t-stuck", func(context.Context) error {
			<-release
			return nil
		})

		start := time.Now()
		assert.False(t, l.wait(loopDrainTimeout))
		assert.Equal(t, loopDrainTimeout, time.Since(start))
		close(release)
		synctest.Wait()
	})
}
