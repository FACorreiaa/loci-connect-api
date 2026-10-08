// Package ai selects provider-specific clients from application configuration.
package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/gemini"
	"github.com/FACorreiaa/loci-connect-api/pkg/openrouter"
)

// NewChatClient builds the chat provider chain.
//
// The chain is: [BYOK] -> [Anthropic] -> primary -> fallbacks. The Anthropic
// slot is a trial link, built only when ANTHROPIC_API_KEY is set and
// ANTHROPIC_FIRST is on; see config.AnthropicConfig. The BYOK slot is
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

	if e, err := newAnthropicEntry(cfg, logger); err != nil {
		errs = append(errs, err)
	} else if e != nil {
		entries = append(entries, e)
	}

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
	return newChainClient(entries, cfg.FallbackCooldown, logger).
		withStreamTimeouts(cfg.StreamFirstChunkTimeout, cfg.StreamIdleTimeout), nil
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

	var (
		client generativeAI.ChatClient
		err    error
	)
	switch provider {
	case config.AIProviderGemini:
		client, err = gemini.NewChatClient(ctx, linkCfg, logger)
	case config.AIProviderOpenRouter:
		client, err = openrouter.NewChatClient(linkCfg, logger)
	default:
		return nil, fmt.Errorf("unsupported AI provider %q", provider)
	}
	if err != nil {
		return nil, err
	}

	// Meter every link in the chain, so tokens are attributed to the model
	// that actually answered rather than to the one that was configured first.
	// Traced the same way, so PostHog AI Observability sees every provider.
	return newTracing(newMetered(client, recordPrometheus), provider), nil
}

// newAnthropicEntry builds the Anthropic trial link, or nil when the trial is
// off. A link that cannot be built is reported and skipped, never fatal: the
// chain behind it is exactly what served before the trial.
func newAnthropicEntry(cfg config.AIConfig, logger *slog.Logger) (*entry, error) {
	if !cfg.Anthropic.Enabled() {
		return nil, nil
	}
	client, err := anthropic.NewChatClient(cfg, logger)
	if err != nil {
		logger.Warn("anthropic llm provider unavailable, skipping it",
			slog.String("model", cfg.Anthropic.Model),
			slog.String("error", err.Error()))
		return nil, fmt.Errorf("anthropic provider %s: %w", cfg.Anthropic.Model, err)
	}
	// Metered and traced like every other link, so the trial's tokens and
	// failovers show up next to the models it is being compared with.
	return &entry{
		client: newTracing(newMetered(client, recordPrometheus), providerAnthropic),
		model:  cfg.Anthropic.Model,
	}, nil
}

// providerAnthropic names the trial link in traces. It is not a config
// provider: AI_PROVIDER never takes this value.
const providerAnthropic = "anthropic"

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
