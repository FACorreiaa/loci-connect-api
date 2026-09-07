package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// NewFreeChatClient builds the chain that serves callers with no provider of
// their own and no paid plan.
//
// It is the fallback list and nothing else. The primary is deliberately absent:
// that key is the one the operator pays per token for, and the point of a free
// floor is that free-tier traffic never reaches it. The paid chain in
// NewChatClient still keeps these same models behind its primary, as a backstop
// for when the primary refuses — so a free model can answer a paying user, but
// a paid model can never answer a free one.
//
// Returns an error rather than a client when there is no floor to build. A
// caller that cannot tell "no free chain" from "a free chain that happens to be
// the paid one" would bill the operator for exactly the traffic this exists to
// keep cheap.
//
// The models themselves are OpenRouter's zero-cost tier, and their rate limits
// are per ACCOUNT rather than per user. One key is therefore one bucket shared
// by every free caller: under load this floor queues and then refuses, which is
// the honest shape of a free tier rather than a defect. AI_FALLBACK_OPENROUTER_API_KEY
// exists so that bucket can be a different account from the operator's paid
// credit.
func NewFreeChatClient(
	ctx context.Context,
	cfg config.AIConfig,
	logger *slog.Logger,
) (generativeAI.ChatClient, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if !cfg.FallbackEnabled {
		return nil, errors.New("no free chat chain: AI_FALLBACK_ENABLED is false")
	}
	if len(cfg.Fallbacks) == 0 {
		return nil, errors.New("no free chat chain: no fallback models are configured")
	}

	var (
		entries []*entry
		errs    []error
	)
	for _, spec := range cfg.Fallbacks {
		client, err := newProviderClient(ctx, spec.Provider, spec.APIKey, spec.Model, cfg, logger)
		if err != nil {
			// One unusable free model is not fatal while another can answer;
			// that is what the chain is for.
			errs = append(errs, fmt.Errorf("free provider %s/%s: %w", spec.Provider, spec.Model, err))
			continue
		}
		entries = append(entries, &entry{client: client, model: spec.Model})
	}

	if len(entries) == 0 {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, errors.New("no usable free chat provider configured")
	}

	logger.Info("free chat chain active",
		slog.Int("providers", len(entries)),
		slog.String("head", entries[0].model))

	// Wrapped even when there is one entry, unlike NewChatClient's single-link
	// shortcut: the cooldown is what stops a rate-limited free model being
	// retried on every request, and a free chain of one is the case where that
	// matters most.
	return newChainClient(entries, cfg.FallbackCooldown, logger), nil
}
