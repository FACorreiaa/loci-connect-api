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
}

var _ generativeAI.ChatClient = (*chainClient)(nil)

func newChainClient(entries []*entry, cooldown time.Duration, logger *slog.Logger) *chainClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &chainClient{entries: entries, cooldown: cooldown, logger: logger}
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

// GenerateStream fails over only until the first chunk reaches the
// consumer. Once any content has been emitted a second provider cannot
// resume a half-written answer, so mid-stream failures surface to the
// caller instead of silently switching models. The underlying clients
// share this limitation: their retry loops also wrap only the initial
// request.
func (c *chainClient) GenerateStream(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	var seq iter.Seq2[*genai.GenerateContentResponse, error]

	err := c.do("generate_stream", func(e *entry) error {
		inner, err := e.client.GenerateStream(ctx, prompt, config)
		if err != nil {
			return err
		}

		// Pull one chunk to convert a lazily-reported provider rejection
		// into a value we can still fail over on. Without this the error
		// only appears once the caller starts ranging, by which point the
		// chain has already committed to this provider.
		next, stop := iter.Pull2(inner)
		head, headErr, ok := next()
		if !ok {
			stop()
			seq = emptySeq
			return nil
		}
		if headErr != nil {
			stop()
			return headErr
		}

		seq = func(yield func(*genai.GenerateContentResponse, error) bool) {
			defer stop()
			if !yield(head, nil) {
				return
			}
			for {
				resp, err, ok := next()
				if !ok {
					return
				}
				if !yield(resp, err) {
					return
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seq, nil
}

func emptySeq(yield func(*genai.GenerateContentResponse, error) bool) {}

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
