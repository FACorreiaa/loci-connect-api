package gemini

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// NewChatClient builds a Gemini chat client with retry policy and logging from config.
func NewChatClient(ctx context.Context, cfg config.GeminiConfig, logger *slog.Logger) (generativeAI.ChatClient, error) {
	client, err := generativeAI.NewGeminiChatClient(ctx, cfg.APIKey, cfg.Model)
	if err != nil {
		return nil, err
	}
	gc, ok := client.(*generativeAI.GeminiChatClient)
	if !ok {
		return client, nil
	}
	return gc.WithRetryPolicy(generativeAI.RetryPolicy{
		MaxRetries: cfg.MaxRetries,
		BaseDelay:  cfg.RetryBaseDelay,
		MaxDelay:   cfg.RetryMaxDelay,
	}).WithLogger(logger), nil
}

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
