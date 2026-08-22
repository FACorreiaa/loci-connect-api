// Package ai selects provider-specific clients from application configuration.
package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/gemini"
	"github.com/FACorreiaa/loci-connect-api/pkg/openrouter"
)

// NewChatClient builds the chat provider chain.
//
// The chain is: [BYOK] -> primary -> fallbacks. The BYOK slot is
// reserved for a future per-user provider key; nothing populates it
// today, so the chain currently starts at the primary. When only one
// link is viable the concrete client is returned unwrapped, so the
// common case (production, fallback disabled) carries no overhead and
// no behaviour change.
func NewChatClient(
	ctx context.Context,
	cfg config.AIConfig,
	logger *slog.Logger,
) (generativeAI.ChatClient, error) {
	if logger == nil {
		logger = slog.Default()
	}

	var (
		entries []*entry
		errs    []error
	)

	// TODO(byok): prepend the caller's own provider key here once
	// per-user credentials exist. It becomes chain index 0 so a user's
	// key is always preferred over the app's.

	if cfg.APIKey != "" {
		client, err := newProviderClient(ctx, cfg.Provider, cfg.APIKey, cfg.Model, cfg, logger)
		if err != nil {
			// Not fatal while a fallback can still answer: a rejected or
			// absent primary key is exactly what the chain exists for.
			errs = append(errs, fmt.Errorf("primary provider %s/%s: %w", cfg.Provider, cfg.Model, err))
			logger.Warn("primary llm provider unavailable, relying on fallbacks",
				slog.String("provider", cfg.Provider),
				slog.String("model", cfg.Model),
				slog.String("error", err.Error()))
		} else {
			entries = append(entries, &entry{client: client, model: cfg.Model})
		}
	}

	for _, spec := range cfg.Fallbacks {
		client, err := newProviderClient(ctx, spec.Provider, spec.APIKey, spec.Model, cfg, logger)
		if err != nil {
			errs = append(errs, fmt.Errorf("fallback provider %s/%s: %w", spec.Provider, spec.Model, err))
			continue
		}
		entries = append(entries, &entry{client: client, model: spec.Model})
	}

	switch len(entries) {
	case 0:
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, errors.New("no usable AI chat provider configured")
	case 1:
		if len(cfg.Fallbacks) == 0 {
			return entries[0].client, nil
		}
	}

	if len(entries) > 1 {
		logger.Info("llm fallback chain active",
			slog.Int("providers", len(entries)),
			slog.String("primary", entries[0].model))
	}
	return newChainClient(entries, cfg.FallbackCooldown, logger), nil
}

// newProviderClient constructs a single provider client. cfg supplies the
// shared retry and timeout tuning; provider, apiKey and model override
// the per-link identity so one AIConfig can build several clients.
func newProviderClient(
	ctx context.Context,
	provider, apiKey, model string,
	cfg config.AIConfig,
	logger *slog.Logger,
) (generativeAI.ChatClient, error) {
	linkCfg := cfg
	linkCfg.Provider = provider
	linkCfg.APIKey = apiKey
	linkCfg.Model = model

	switch provider {
	case config.AIProviderGemini:
		return gemini.NewChatClient(ctx, linkCfg, logger)
	case config.AIProviderOpenRouter:
		return openrouter.NewChatClient(linkCfg, logger)
	default:
		return nil, fmt.Errorf("unsupported AI provider %q", provider)
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
