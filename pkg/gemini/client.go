package gemini

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"time"

	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// NewChatClient builds a Gemini chat client with retry policy and logging from
// config, wrapped with per-call timeouts and typed error classification.
func NewChatClient(ctx context.Context, cfg config.GeminiConfig, logger *slog.Logger) (generativeAI.ChatClient, error) {
	client, err := generativeAI.NewGeminiChatClient(ctx, cfg.APIKey, cfg.Model)
	if err != nil {
		return nil, err
	}
	if gc, ok := client.(*generativeAI.GeminiChatClient); ok {
		client = gc.WithRetryPolicy(generativeAI.RetryPolicy{
			MaxRetries: cfg.MaxRetries,
			BaseDelay:  cfg.RetryBaseDelay,
			MaxDelay:   cfg.RetryMaxDelay,
		}).WithLogger(logger)
	}
	return &guardedClient{
		inner:           client,
		generateTimeout: cfg.GenerateTimeout,
		streamTimeout:   cfg.StreamTimeout,
	}, nil
}

// guardedClient enforces per-call deadlines independent of the RPC deadline
// (stream workers often run on detached contexts) and classifies provider
// errors into pkg/llmerrors sentinels.
type guardedClient struct {
	inner           generativeAI.ChatClient
	generateTimeout time.Duration
	streamTimeout   time.Duration
}

var _ generativeAI.ChatClient = (*guardedClient)(nil)

func (g *guardedClient) Generate(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	if g.generateTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.generateTimeout)
		defer cancel()
	}
	resp, err := g.inner.Generate(ctx, prompt, cfg)
	return resp, llmerrors.Classify(err)
}

func (g *guardedClient) GenerateText(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig) (string, error) {
	if g.generateTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.generateTimeout)
		defer cancel()
	}
	txt, err := g.inner.GenerateText(ctx, prompt, cfg)
	return txt, llmerrors.Classify(err)
}

func (g *guardedClient) GenerateStream(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	cancel := context.CancelFunc(func() {})
	if g.streamTimeout > 0 {
		// The deadline must outlive this call and cap the whole stream;
		// cancel runs when iteration finishes.
		ctx, cancel = context.WithTimeout(ctx, g.streamTimeout)
	}
	seq, err := g.inner.GenerateStream(ctx, prompt, cfg)
	if err != nil {
		cancel()
		return nil, llmerrors.Classify(err)
	}
	guarded := func(yield func(*genai.GenerateContentResponse, error) bool) {
		defer cancel()
		for resp, err := range seq {
			if !yield(resp, llmerrors.Classify(err)) {
				return
			}
		}
	}
	return guarded, nil
}

func (g *guardedClient) StartChatSession(ctx context.Context, cfg *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return g.inner.StartChatSession(ctx, cfg)
}

func (g *guardedClient) Model() string { return g.inner.Model() }

func (g *guardedClient) Close() error { return g.inner.Close() }

// RetryPolicyFromConfig maps GeminiConfig retry fields to the SDK policy.
func RetryPolicyFromConfig(cfg config.GeminiConfig) generativeAI.RetryPolicy {
	return generativeAI.RetryPolicy{
		MaxRetries: cfg.MaxRetries,
		BaseDelay:  cfg.RetryBaseDelay,
		MaxDelay:   cfg.RetryMaxDelay,
	}
}

// ValidateRetryConfig returns an error when retry delays are inconsistent.
func ValidateRetryConfig(cfg config.GeminiConfig) error {
	if cfg.MaxRetries < 0 {
		return fmt.Errorf("GEMINI_MAX_RETRIES must be >= 0")
	}
	if cfg.RetryBaseDelay < 0 || cfg.RetryMaxDelay < 0 {
		return fmt.Errorf("retry delays must be non-negative")
	}
	if cfg.RetryMaxDelay > 0 && cfg.RetryBaseDelay > cfg.RetryMaxDelay {
		return fmt.Errorf("GEMINI_RETRY_BASE_DELAY must not exceed GEMINI_RETRY_MAX_DELAY")
	}
	return nil
}

// DefaultRetryBaseDelay matches go-genai-sdk DefaultRetryPolicy.
const DefaultRetryBaseDelay = 500 * time.Millisecond

// DefaultRetryMaxDelay matches go-genai-sdk DefaultRetryPolicy.
const DefaultRetryMaxDelay = 8 * time.Second
