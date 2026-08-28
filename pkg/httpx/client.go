// Package httpx is the shared outbound HTTP client for third-party data
// providers (weather, hazards, holidays, air quality, news, FX).
//
// It exists because the server had no such thing: the only real outbound client
// was the OpenWeather adapter, which had no retry and no rate limiting, and the
// only retry implementation lived welded inside pkg/openrouter. Adding several
// public data sources — some of which publish usage policies and expect an
// identifying User-Agent — made that untenable.
//
// Deliberately small. It does four things: a timeout, a bounded retry that
// honours Retry-After, a per-host outbound rate limit, and a metric per call.
// It is not a circuit breaker and not a cache; caching belongs with the adapter
// that knows the right TTL for its data.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Defaults chosen to be polite to free public APIs rather than fast.
const (
	DefaultTimeout       = 8 * time.Second
	DefaultMaxRetries    = 2
	DefaultBaseDelay     = 300 * time.Millisecond
	DefaultMaxDelay      = 5 * time.Second
	DefaultRatePerSecond = 5
	DefaultBurst         = 5
	// maxErrorBodyBytes caps how much of an error response we read into a
	// message. Some providers answer a 500 with an HTML page.
	maxErrorBodyBytes = 512
)

// Config configures a Client. The zero value is usable: every field falls back
// to the Default* constants above.
type Config struct {
	Timeout       time.Duration
	MaxRetries    int
	BaseDelay     time.Duration
	MaxDelay      time.Duration
	RatePerSecond float64
	Burst         int
	// UserAgent identifies us to providers. Several public APIs (OSM-derived
	// services in particular) require a real one and rate-limit or block
	// requests without it, so this is not optional decoration.
	UserAgent string
}

func (c Config) withDefaults() Config {
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = DefaultMaxRetries
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = DefaultBaseDelay
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = DefaultMaxDelay
	}
	if c.RatePerSecond <= 0 {
		c.RatePerSecond = DefaultRatePerSecond
	}
	if c.Burst <= 0 {
		c.Burst = DefaultBurst
	}
	if c.UserAgent == "" {
		c.UserAgent = "loci/1.0"
	}
	return c
}

// Client is a rate-limited, retrying HTTP client. Safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client

	mu       sync.Mutex
	limiters map[string]*rate.Limiter

	// observe is called once per completed attempt. Injected rather than
	// imported so this package stays free of a Prometheus dependency and stays
	// trivially testable.
	observe func(source, outcome string, d time.Duration)
}

// New builds a Client.
func New(cfg Config) *Client {
	cfg = cfg.withDefaults()
	return &Client{
		cfg:      cfg,
		http:     &http.Client{Timeout: cfg.Timeout},
		limiters: make(map[string]*rate.Limiter),
	}
}

// WithObserver attaches a callback invoked once per attempt with the logical
// source name, an outcome label and the elapsed time.
func (c *Client) WithObserver(f func(source, outcome string, d time.Duration)) *Client {
	c.observe = f
	return c
}

// StatusError is returned when a provider answers with a non-2xx status that we
// gave up on. Callers can inspect Status to distinguish "provider is down" from
// "we asked for something that does not exist".
type StatusError struct {
	Source string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s: status %d", e.Source, e.Status)
	}
	return fmt.Sprintf("%s: status %d: %s", e.Source, e.Status, e.Body)
}

// limiterFor returns the rate limiter for a host, creating it on first use.
// Limiting is per-host rather than global so a slow provider cannot starve the
// others sharing this client.
func (c *Client) limiterFor(host string) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.limiters[host]; ok {
		return l
	}
	l := rate.NewLimiter(rate.Limit(c.cfg.RatePerSecond), c.cfg.Burst)
	c.limiters[host] = l
	return l
}

// Get issues a rate-limited GET with retries and returns the response body.
//
// `source` is the logical provider name used for metrics and error messages
// ("open-meteo", "usgs", "nager"), not a URL.
func (c *Client) Get(ctx context.Context, source, url string) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		req.Header.Set("Accept", "application/json")

		// Wait for a token before every attempt, including retries — a retry
		// that ignored the limiter would be exactly the burst the limit exists
		// to prevent.
		if err := c.limiterFor(req.URL.Host).Wait(ctx); err != nil {
			return nil, err
		}

		started := time.Now()
		resp, err := c.http.Do(req)
		elapsed := time.Since(started)

		if err != nil {
			c.record(source, "transport_error", elapsed)
			lastErr = fmt.Errorf("%s request: %w", source, err)
			// A cancelled or expired context will not succeed on a retry, and
			// retrying it would burn the caller's remaining deadline.
			if ctx.Err() != nil {
				return nil, lastErr
			}
			if attempt < c.cfg.MaxRetries {
				if werr := wait(ctx, retryDelay("", c.cfg.BaseDelay, c.cfg.MaxDelay, attempt)); werr != nil {
					return nil, werr
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode == http.StatusOK {
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr != nil {
				c.record(source, "read_error", elapsed)
				return nil, fmt.Errorf("%s read body: %w", source, rerr)
			}
			c.record(source, "ok", elapsed)
			return body, nil
		}

		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()

		statusErr := &StatusError{Source: source, Status: resp.StatusCode, Body: string(snippet)}
		lastErr = statusErr

		if !isRetryableStatus(resp.StatusCode) {
			c.record(source, "client_error", elapsed)
			return nil, statusErr
		}

		c.record(source, "retryable_error", elapsed)
		if attempt < c.cfg.MaxRetries {
			if werr := wait(ctx, retryDelay(retryAfter, c.cfg.BaseDelay, c.cfg.MaxDelay, attempt)); werr != nil {
				return nil, werr
			}
			continue
		}
	}

	if lastErr == nil {
		lastErr = errors.New(source + ": exhausted retries")
	}
	return nil, lastErr
}

func (c *Client) record(source, outcome string, d time.Duration) {
	if c.observe != nil {
		c.observe(source, outcome, d)
	}
}

// isRetryableStatus mirrors pkg/openrouter: 429 and any 5xx are worth another
// attempt; a 4xx means we asked wrongly and will keep asking wrongly.
func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// retryDelay honours an explicit Retry-After when the provider sends one and
// falls back to exponential backoff. Lifted from pkg/openrouter/chat.go, which
// already got this right; keeping the behaviour identical means one rule for
// outbound backoff across the server.
func retryDelay(retryAfter string, baseDelay, maxDelay time.Duration, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		delay := time.Duration(seconds) * time.Second
		if maxDelay <= 0 || delay <= maxDelay {
			return delay
		}
		return maxDelay
	}
	delay := baseDelay
	for range attempt {
		delay *= 2
		if maxDelay > 0 && delay >= maxDelay {
			return maxDelay
		}
	}
	return delay
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
