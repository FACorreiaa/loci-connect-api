package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The MCP endpoint is mounted outside the Connect interceptor chain, so it does
// not inherit the RPC timeouts. Without a deadline of its own a tool that hangs
// holds its connection and goroutine for as long as the client will wait, and
// the server's WriteTimeout is deliberately 0 for streaming.
func TestTimeoutMiddlewareBoundsASlowTool(t *testing.T) {
	started := make(chan struct{})
	observed := make(chan error, 1)
	slow := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		observed <- r.Context().Err()
	})

	srv := httptest.NewServer(withTimeout(20*time.Millisecond, slow))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	<-started
	select {
	case ctxErr := <-observed:
		if ctxErr != context.DeadlineExceeded {
			t.Fatalf("handler context error = %v, want %v", ctxErr, context.DeadlineExceeded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler context was never cancelled; the request is unbounded")
	}
}

func TestTimeoutMiddlewareLeavesFastToolsAlone(t *testing.T) {
	fast := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := httptest.NewServer(withTimeout(2*time.Second, fast))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

// A zero or negative timeout means "no deadline configured"; the middleware
// must then be a pass-through rather than cancelling everything instantly.
func TestTimeoutMiddlewareDisabledWhenNotConfigured(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	srv := httptest.NewServer(withTimeout(0, inner))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 (handler reached unchanged)", resp.StatusCode)
	}
}

// The deadline fires on a different goroutine than the tool it interrupts, so
// the "has the response started" flag is read and written concurrently exactly
// when a slow tool is mid-write. Run with -race.
func TestTimeoutMiddlewareDoesNotRaceWithAWritingTool(t *testing.T) {
	writing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 200; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			_, _ = w.Write([]byte("chunk"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})

	srv := httptest.NewServer(withTimeout(time.Millisecond, writing))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
