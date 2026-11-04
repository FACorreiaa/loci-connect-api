package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// The pod runs on Kubernetes' default 30s grace period, and SIGKILL at the end
// of it loses the cleanup. These two leave 10s for deps.Cleanup and the telemetry
// flushes. A stream still open after the drain is cut off, as it would be anyway.
const (
	shutdownTimeout  = 15 * time.Second
	loopDrainTimeout = 5 * time.Second
)

// Restart delays after a loop panics: doubling from the base, up to the max. A
// run that lasted at least the max starts again from the base.
const (
	loopRestartBase = time.Second
	loopRestartMax  = time.Minute
)

// serve runs srv until ctx is cancelled, then drains it for up to timeout.
//
// A drain that times out is not a failure: any open stream causes one, and
// returning an error for it made main exit without running its cleanup. Only a
// server that could not run is reported.
func serve(ctx context.Context, srv *http.Server, lis net.Listener, timeout time.Duration, logger *slog.Logger) error {
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- srv.Serve(lis) }()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down", "cause", context.Cause(ctx))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("drain did not finish; closing open connections", "timeout", timeout, "error", err)
		if err := srv.Close(); err != nil {
			logger.Warn("closing open connections failed", "error", err)
		}
		return nil
	}
	logger.Info("server stopped gracefully")
	return nil
}

// loops supervises the background loops that live as long as the API does.
type loops struct {
	wg     sync.WaitGroup
	logger *slog.Logger
}

func newLoops(logger *slog.Logger) *loops { return &loops{logger: logger} }

// start runs a loop until ctx is cancelled.
//
// A panic restarts it after a delay, since until now one panic ended the loop
// until the next deploy. An error is the loop's own decision to stop, such as a
// rejected bot token, so it stays stopped. A nil return means it is switched off
// by configuration. No outcome takes the API down: the app still answers without
// them.
func (l *loops) start(ctx context.Context, name string, run func(context.Context) error) {
	l.wg.Go(func() {
		delay := loopRestartBase
		for {
			started := time.Now()
			panicked, err := l.runOnce(ctx, name, run)
			if !panicked {
				if err != nil && ctx.Err() == nil {
					l.logger.Error("background loop stopped", "loop", name, "error", err)
					observability.BackgroundLoopStopsTotal.WithLabelValues(name, "error").Inc()
				}
				return
			}

			if time.Since(started) >= loopRestartMax {
				delay = loopRestartBase
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			delay = min(delay*2, loopRestartMax)
		}
	})
}

func (l *loops) runOnce(ctx context.Context, name string, run func(context.Context) error) (panicked bool, err error) {
	up := observability.BackgroundLoopUp.WithLabelValues(name)
	up.Set(1)
	defer up.Set(0)
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			l.logger.Error("background loop panicked; restarting",
				"loop", name, "recover", r, "stack", string(debug.Stack()))
			observability.BackgroundLoopStopsTotal.WithLabelValues(name, "panic").Inc()
		}
	}()
	return false, run(ctx)
}

// wait reports whether every loop ended within timeout. Giving up matters: a
// stuck loop must not hold the process until Kubernetes kills it.
func (l *loops) wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
