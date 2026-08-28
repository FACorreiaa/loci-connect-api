package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastClient(t *testing.T) Config {
	t.Helper()
	return Config{
		Timeout:       2 * time.Second,
		MaxRetries:    2,
		BaseDelay:     time.Millisecond,
		MaxDelay:      5 * time.Millisecond,
		RatePerSecond: 1000,
		Burst:         1000,
		UserAgent:     "loci-test/1.0",
	}
}

func TestGet_ReturnsBodyOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := New(fastClient(t)).Get(context.Background(), "test", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("got %q", body)
	}
}

// Several public data APIs require an identifying User-Agent and throttle or
// block requests without one, so this is a functional requirement.
func TestGet_SendsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := New(fastClient(t)).Get(context.Background(), "test", srv.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "loci-test/1.0" {
		t.Errorf("User-Agent: got %q, want loci-test/1.0", got)
	}
}

func TestGet_RetriesRetryableStatusThenSucceeds(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := New(fastClient(t)).Get(context.Background(), "test", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("got %q", body)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

// A 4xx means we asked wrongly; asking again wastes the caller's deadline and
// the provider's quota.
func TestGet_DoesNotRetryClientErrors(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad params"))
	}))
	defer srv.Close()

	_, err := New(fastClient(t)).Get(context.Background(), "test", srv.URL)
	if err == nil {
		t.Fatal("expected an error")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected a *StatusError, got %T", err)
	}
	if se.Status != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", se.Status)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 attempt, got %d", calls)
	}
}

func TestGet_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := fastClient(t)
	cfg.MaxRetries = 2
	if _, err := New(cfg).Get(context.Background(), "test", srv.URL); err == nil {
		t.Fatal("expected an error")
	}
	// 1 initial attempt + 2 retries.
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

// Honouring Retry-After is the difference between backing off politely and
// getting a free API to block us.
func TestGet_HonoursRetryAfterHeader(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := fastClient(t)
	cfg.MaxDelay = 2 * time.Second // let the 1s Retry-After through

	started := time.Now()
	if _, err := New(cfg).Get(context.Background(), "test", srv.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Errorf("expected to wait the 1s Retry-After, waited %v", elapsed)
	}
}

// A Retry-After longer than MaxDelay must be capped, or one provider can pin a
// request open for minutes.
func TestRetryDelay_CapsRetryAfterAtMaxDelay(t *testing.T) {
	got := retryDelay("3600", time.Millisecond, 5*time.Second, 0)
	if got != 5*time.Second {
		t.Errorf("got %v, want the 5s cap", got)
	}
}

func TestRetryDelay_BacksOffExponentially(t *testing.T) {
	base, max := 100*time.Millisecond, 10*time.Second
	a0 := retryDelay("", base, max, 0)
	a1 := retryDelay("", base, max, 1)
	a2 := retryDelay("", base, max, 2)
	if a0 >= a1 || a1 >= a2 {
		t.Errorf("expected increasing delays, got %v %v %v", a0, a1, a2)
	}
	if got := retryDelay("", base, max, 20); got != max {
		t.Errorf("expected the cap at high attempts, got %v", got)
	}
}

// The limiter must apply to retries too — a retry storm is exactly the burst
// the limit exists to prevent.
func TestGet_RateLimitsPerHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := fastClient(t)
	cfg.RatePerSecond = 10 // 100ms apart
	cfg.Burst = 1
	c := New(cfg)
	ctx := context.Background()

	started := time.Now()
	for range 3 {
		if _, err := c.Get(ctx, "test", srv.URL); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// First is free (burst 1), the next two wait ~100ms each.
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond {
		t.Errorf("expected rate limiting to slow 3 calls, took %v", elapsed)
	}
}

// A cancelled context must not be retried — it cannot succeed and retrying
// burns whatever deadline the caller had left.
func TestGet_CancelledContextIsNotRetried(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := New(fastClient(t)).Get(ctx, "test", srv.URL); err == nil {
		t.Fatal("expected an error")
	}
	if calls > 1 {
		t.Errorf("expected no retry after context expiry, got %d attempts", calls)
	}
}

func TestGet_ObserverSeesEveryAttempt(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var outcomes []string
	c := New(fastClient(t)).WithObserver(func(source, outcome string, d time.Duration) {
		if source != "test" {
			t.Errorf("source: got %q", source)
		}
		outcomes = append(outcomes, outcome)
	})

	if _, err := c.Get(context.Background(), "test", srv.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"retryable_error", "ok"}
	if len(outcomes) != len(want) {
		t.Fatalf("outcomes: got %v, want %v", outcomes, want)
	}
	for i := range want {
		if outcomes[i] != want[i] {
			t.Errorf("outcome %d: got %q, want %q", i, outcomes[i], want[i])
		}
	}
}

func TestGetJSON_DecodesInto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"Lisbon","n":3}`))
	}))
	defer srv.Close()

	type payload struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	got, err := GetJSON[payload](context.Background(), New(fastClient(t)), "test", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "Lisbon" || got.N != 3 {
		t.Errorf("got %+v", got)
	}
}

// Providers answering an error with an HTML page is common; the message must
// name the provider and show what it actually sent.
func TestGetJSON_HTMLBodyProducesAUsefulError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>down for maintenance</body></html>`))
	}))
	defer srv.Close()

	_, err := GetJSON[map[string]any](context.Background(), New(fastClient(t)), "gdacs", srv.URL)
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if !strings.Contains(err.Error(), "gdacs") {
		t.Errorf("error should name the provider: %v", err)
	}
	if !strings.Contains(err.Error(), "maintenance") {
		t.Errorf("error should include the body snippet: %v", err)
	}
}

// The zero Config must produce a working client, since that is what a caller
// gets when they configure nothing.
func TestNew_ZeroConfigIsUsable(t *testing.T) {
	c := New(Config{})
	if c.cfg.Timeout != DefaultTimeout {
		t.Errorf("timeout: got %v", c.cfg.Timeout)
	}
	if c.cfg.UserAgent == "" {
		t.Error("a User-Agent must always be set")
	}
	if c.cfg.RatePerSecond <= 0 || c.cfg.Burst <= 0 {
		t.Error("rate limiting must always be configured")
	}
}
