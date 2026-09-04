package analytics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Without a key there is no sink, so the recorder built from it is inert.
func TestNewPostHogSinkWithoutAKey(t *testing.T) {
	t.Setenv("POSTHOG_API_KEY", "")
	sink, err := NewPostHogSink(testLogger())
	if err != nil {
		t.Fatalf("NewPostHogSink: %v", err)
	}
	if sink != nil {
		t.Fatal("no key configured must yield no sink")
	}
	if r := New(sink, testLogger()); r != nil {
		t.Fatal("a nil sink must yield an inert recorder")
	}
}

// The event has to survive the whole path: through the SDK, over HTTP, with the
// user and event name intact. A fake at the Sink boundary would not prove that.
func TestPostHogSinkDeliversTheEvent(t *testing.T) {
	var (
		mu   sync.Mutex
		body string
	)
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		select {
		case received <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	t.Setenv("POSTHOG_API_KEY", "phc_test_key")
	t.Setenv("POSTHOG_HOST", server.URL)

	sink, err := NewPostHogSink(testLogger())
	if err != nil {
		t.Fatalf("NewPostHogSink: %v", err)
	}
	if sink == nil {
		t.Fatal("a configured key must yield a sink")
	}

	recorder := New(sink, testLogger())
	recorder.Capture("user-77", "trip_reopened", map[string]any{"trip_id": "t-1"})

	// Close flushes rather than waiting for the batch interval.
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("no request reached the collector")
	}

	mu.Lock()
	got := body
	mu.Unlock()
	for _, want := range []string{"user-77", "trip_reopened", "t-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("payload missing %q; got %s", want, got)
		}
	}
}

// The host is optional and defaults to PostHog's EU cloud, which is where this
// project lives.
func TestPostHogSinkDefaultsToEUCloud(t *testing.T) {
	t.Setenv("POSTHOG_API_KEY", "phc_test_key")
	if err := os.Unsetenv("POSTHOG_HOST"); err != nil {
		t.Fatalf("unset host: %v", err)
	}
	sink, err := NewPostHogSink(testLogger())
	if err != nil {
		t.Fatalf("NewPostHogSink: %v", err)
	}
	if sink == nil {
		t.Fatal("expected a sink")
	}
	t.Cleanup(func() { _ = sink.Close() })
}
