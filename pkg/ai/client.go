// Package ai selects provider-specific clients from application configuration.
package ai

import (
	"context"
	"fmt"
	"log/slog"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/gemini"
	"github.com/FACorreiaa/loci-connect-api/pkg/openrouter"
)

func NewChatClient(
	ctx context.Context,
	cfg config.AIConfig,
	logger *slog.Logger,
) (generativeAI.ChatClient, error) {
	switch cfg.Provider {
	case config.AIProviderGemini:
		return gemini.NewChatClient(ctx, cfg, logger)
	case config.AIProviderOpenRouter:
		return openrouter.NewChatClient(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported AI provider %q", cfg.Provider)
	}
}

func NewEmbeddingClient(
	ctx context.Context,
	cfg config.AIConfig,
	logger *slog.Logger,
) (generativeAI.EmbeddingClient, error) {
	switch cfg.Provider {
	case config.AIProviderGemini:
		client, err := generativeAI.NewGeminiEmbeddingClient(
			ctx,
			cfg.APIKey,
			cfg.EmbeddingModel,
			logger,
		)
		if err != nil {
			return nil, err
		}
		if geminiClient, ok := client.(*generativeAI.GeminiEmbeddingClient); ok {
			client = geminiClient.WithRetryPolicy(gemini.RetryPolicyFromConfig(cfg))
		}
		return client, nil
	case config.AIProviderOpenRouter:
		return openrouter.NewEmbeddingClient(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported AI provider %q", cfg.Provider)
	}
}
