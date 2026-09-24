package ai

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// entry is one link in the chain: a constructed client plus the cooldown
// state that keeps a dead credential from being retried on every request.
type entry struct {
	client generativeAI.ChatClient
	model  string
	// deadUntil holds a UnixNano deadline. Zero means usable. It is read
	// and written from concurrent request goroutines, hence atomic.
	deadUntil atomic.Int64
}

func (e *entry) usable(now time.Time) bool {
	until := e.deadUntil.Load()
	return until == 0 || now.UnixNano() >= until
}

func (e *entry) markDead(now time.Time, cooldown time.Duration) {
	if cooldown > 0 {
		e.deadUntil.Store(now.Add(cooldown).UnixNano())
	}
}

// chainClient tries a sequence of chat providers in order, advancing when
// one fails with a provider-side error. It exists so the app keeps
// answering when the primary credential is missing, rejected, or out of
// credits — the free-model links are a local testing floor, not a
// production serving path.
//
// The chain deliberately does not retry within a provider: both concrete
// clients already run their own retry loops for 429/5xx, and double
// retrying multiplies latency on a request that is already failing.
type chainClient struct {
	entries  []*entry
	cooldown time.Duration
	logger   *slog.Logger
	// active tracks which entry most recently answered, so Model() and
	// telemetry report the model that actually produced the response
	// rather than the one that was configured.
	active atomic.Int32

	// firstChunkTimeout bounds the wait for a stream's first content, and
	// idleTimeout the gap between chunks after that. Both exist because a
	// provider can accept a request and then say nothing: the free tier's
	// nex-agi model did exactly that in production, "serving" an itinerary
	// for 90 seconds without a byte, while the only other bound was the
	// two-minute whole-stream cap. Zero disables the check.
	firstChunkTimeout time.Duration
	idleTimeout       time.Duration
}

var _ generativeAI.ChatClient = (*chainClient)(nil)

// Default stream patience. First content within 25s covers a cold free
// model with a 3k-token prompt; 30s between chunks is an order of magnitude
// above the sub-second gaps a healthy SSE stream shows.
const (
	defaultFirstChunkTimeout = 25 * time.Second
	defaultIdleTimeout       = 30 * time.Second
)

func newChainClient(entries []*entry, cooldown time.Duration, logger *slog.Logger) *chainClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &chainClient{
		entries:           entries,
		cooldown:          cooldown,
		logger:            logger,
		firstChunkTimeout: defaultFirstChunkTimeout,
		idleTimeout:       defaultIdleTimeout,
	}
}

// withStreamTimeouts overrides the stream patience. Zero for either keeps
// the default; a negative value disables that check.
func (c *chainClient) withStreamTimeouts(firstChunk, idle time.Duration) *chainClient {
	if firstChunk != 0 {
		c.firstChunkTimeout = firstChunk
	}
	if idle != 0 {
		c.idleTimeout = idle
	}
	return c
}

// errAllProvidersFailed is returned when no entry could serve the call.
var errAllProvidersFailed = errors.New("all llm providers failed")

// reasonFor labels an error for metrics. Kept to a small closed set so
// the label stays bounded — provider error text must never reach a
// Prometheus label.
func reasonFor(err error) string {
	switch {
	case errors.Is(err, llmerrors.ErrOutOfCredits):
		return "out_of_credits"
	case errors.Is(err, llmerrors.ErrAuthFailed):
		return "auth_failed"
	case errors.Is(err, llmerrors.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, llmerrors.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, llmerrors.ErrStreamStalled):
		return "stalled"
	case errors.Is(err, llmerrors.ErrEmptyResponse):
		return "empty"
	default:
		return "unknown"
	}
}

// do runs fn against each usable entry in order. It stops at the first
// success, and at the first error that failing over cannot fix (a bad
// prompt, a cancelled context) rather than replaying it down the chain.
func (c *chainClient) do(op string, fn func(e *entry) error) error {
	now := time.Now()
	var errs []error
	skipped := 0
	// lastReason describes why the previous entry gave up, so a success
	// here can be attributed to the cause that pushed us down the chain.
	lastReason := "unknown"

	for i, e := range c.entries {
		if !e.usable(now) {
			skipped++
			continue
		}

		err := fn(e)
		if err == nil {
			c.active.Store(int32(i))
			if i > 0 {
				c.logger.Info("llm fallback served request",
					slog.String("op", op),
					slog.String("model", e.model),
					slog.Int("chain_index", i))
				observability.FallbackActivationsTotal.
					WithLabelValues(c.entries[0].model, e.model, lastReason).
					Inc()
			}
			return nil
		}

		lastReason = reasonFor(err)
		errs = append(errs, fmt.Errorf("%s: %w", e.model, err))

		if !llmerrors.Failover(err) {
			// Cancellation, or a request the next provider would reject
			// identically. Surface it as-is.
			return err
		}
		if llmerrors.Terminal(err) {
			e.markDead(now, c.cooldown)
			observability.FallbackProviderBenchedTotal.
				WithLabelValues(e.model, lastReason).
				Inc()
			c.logger.Warn("llm provider credential unusable, cooling down",
				slog.String("op", op),
				slog.String("model", e.model),
				slog.Duration("cooldown", c.cooldown),
				slog.String("error", err.Error()))
		} else {
			c.logger.Warn("llm provider failed, trying next",
				slog.String("op", op),
				slog.String("model", e.model),
				slog.String("error", err.Error()))
		}
	}

	if len(errs) == 0 {
		return fmt.Errorf("%w: all %d providers in cooldown", errAllProvidersFailed, skipped)
	}
	return fmt.Errorf("%w: %w", errAllProvidersFailed, errors.Join(errs...))
}

func (c *chainClient) Generate(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	var out *genai.GenerateContentResponse
	err := c.do("generate", func(e *entry) error {
		resp, err := e.client.Generate(ctx, prompt, config)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (c *chainClient) GenerateText(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (string, error) {
	var out string
	err := c.do("generate_text", func(e *entry) error {
		text, err := e.client.GenerateText(ctx, prompt, config)
		if err != nil {
			return err
		}
		out = text
		return nil
	})
	return out, err
}

// GenerateStream fails over only until the first content reaches the
// consumer. Once any text has been emitted a second provider cannot
// resume a half-written answer, so mid-stream failures surface to the
// caller instead of silently switching models. The underlying clients
// share this limitation: their retry loops also wrap only the initial
// request.
//
// "First content" rather than "first chunk": providers send content-less
// preamble (role markers, usage, a reasoning model's silence) that commits
// the chain to nothing, so it is skipped and the clock keeps running. A
// provider that never gets past the preamble, ends without text, or goes
// quiet mid-answer is reported as ErrStreamStalled / ErrEmptyResponse —
// the first two fail over, the last surfaces.
func (c *chainClient) GenerateStream(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	var seq iter.Seq2[*genai.GenerateContentResponse, error]

	err := c.do("generate_stream", func(e *entry) error {
		s, err := c.openStream(ctx, e, prompt, config)
		if err != nil {
			return err
		}
		seq = s
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seq, nil
}

// openStream starts e's stream and waits for its first content. The
// returned sequence replays that first chunk and then relays the rest,
// watching the gap between chunks.
func (c *chainClient) openStream(
	ctx context.Context,
	e *entry,
	prompt string,
	config *genai.GenerateContentConfig,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	// A private context so a stalled provider can be cut loose without
	// touching the caller's. Cancelling it is what unblocks the read the
	// stalled goroutine is sitting in.
	ectx, cancel := context.WithCancel(ctx)
	inner, err := e.client.GenerateStream(ectx, prompt, config)
	if err != nil {
		cancel()
		return nil, err
	}
	p := newPuller(inner, cancel)

	var head *genai.GenerateContentResponse
	for head == nil {
		got, stalled := p.pull(c.firstChunkTimeout)
		switch {
		case stalled:
			p.close()
			return nil, fmt.Errorf("%w: no content within %s", llmerrors.ErrStreamStalled, c.firstChunkTimeout)
		case !got.ok:
			p.close()
			return nil, fmt.Errorf("%w: stream ended before any text", llmerrors.ErrEmptyResponse)
		case got.err != nil:
			p.close()
			if ctx.Err() != nil {
				// The caller left; report that, not whatever the provider
				// said about its connection being cut.
				return nil, ctx.Err()
			}
			return nil, got.err
		case hasText(got.resp):
			head = got.resp
		}
	}

	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		defer p.close()
		if !yield(head, nil) {
			return
		}
		for {
			got, stalled := p.pull(c.idleTimeout)
			if stalled {
				yield(nil, fmt.Errorf("%w: no chunk for %s", llmerrors.ErrStreamStalled, c.idleTimeout))
				return
			}
			if !got.ok {
				return
			}
			if !yield(got.resp, got.err) {
				return
			}
		}
	}, nil
}

// hasText reports whether resp carries any answer text.
func hasText(resp *genai.GenerateContentResponse) bool {
	if resp == nil {
		return false
	}
	for _, cand := range resp.Candidates {
		if cand == nil || cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part != nil && part.Text != "" {
				return true
			}
		}
	}
	return false
}

// puller wraps iter.Pull2 with a deadline per pull. next and stop are
// never called concurrently: a timed-out pull cancels the provider and
// then waits for the in-flight next to return before anything else runs.
type puller struct {
	next   func() (*genai.GenerateContentResponse, error, bool)
	stop   func()
	cancel context.CancelFunc
}

type pulled struct {
	resp *genai.GenerateContentResponse
	err  error
	ok   bool
}

func newPuller(seq iter.Seq2[*genai.GenerateContentResponse, error], cancel context.CancelFunc) *puller {
	next, stop := iter.Pull2(seq)
	return &puller{next: next, stop: stop, cancel: cancel}
}

// pull returns the next value, or stalled=true when none arrived within d.
// d <= 0 waits without limit.
func (p *puller) pull(d time.Duration) (got pulled, stalled bool) {
	if d <= 0 {
		got.resp, got.err, got.ok = p.next()
		return got, false
	}
	ch := make(chan pulled, 1)
	go func() {
		r, e, ok := p.next()
		ch <- pulled{r, e, ok}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case got = <-ch:
		return got, false
	case <-timer.C:
		p.cancel()
		<-ch
		return pulled{}, true
	}
}

func (p *puller) close() {
	p.cancel()
	p.stop()
}

// StartChatSession is not chained. A session holds provider-side
// conversation state that cannot be migrated mid-conversation, so it
// binds to the first usable provider and stays there.
func (c *chainClient) StartChatSession(
	ctx context.Context,
	config *genai.GenerateContentConfig,
) (*generativeAI.ChatSession, error) {
	var out *generativeAI.ChatSession
	err := c.do("start_session", func(e *entry) error {
		session, err := e.client.StartChatSession(ctx, config)
		if err != nil {
			return err
		}
		out = session
		return nil
	})
	return out, err
}

// Model reports the model that most recently answered, so logs and
// metrics attribute responses to the provider that actually served them.
func (c *chainClient) Model() string {
	idx := int(c.active.Load())
	if idx < 0 || idx >= len(c.entries) {
		return ""
	}
	return c.entries[idx].model
}

func (c *chainClient) Close() error {
	var errs []error
	for _, e := range c.entries {
		if err := e.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
