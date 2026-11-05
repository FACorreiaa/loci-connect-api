package ai

import (
	"context"
	"log/slog"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// ToolClient calls Anthropic with tools. It sits outside the chat chain, which
// is one prompt string and has no other provider that could take over a tool
// call, so it does here what newMetered and newTracing do there: without this a
// tool-using path would be missing from the token metrics and from PostHog.
// Prompts and answers stay out of the span; they carry a person's trips.
type ToolClient struct {
	inner *anthropic.ChatClient
	model string
}

// NewToolClient returns nil, with no error, when Anthropic is not switched on.
func NewToolClient(cfg config.AIConfig, logger *slog.Logger) (*ToolClient, error) {
	if !cfg.Anthropic.Enabled() {
		return nil, nil //nolint:nilnil // no link is a normal configuration
	}
	inner, err := anthropic.NewChatClient(cfg, logger)
	if err != nil {
		return nil, err
	}
	return &ToolClient{inner: inner, model: cfg.Anthropic.Model}, nil
}

// ToolTurn makes one metered, traced model call.
func (c *ToolClient) ToolTurn(
	ctx context.Context,
	req anthropic.ToolTurnRequest,
	onText func(string),
) (*anthropic.ToolTurnResult, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "chat "+c.model)
	defer span.End()
	span.SetAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", providerAnthropic),
		attribute.String("gen_ai.request.model", c.model),
	)

	res, err := c.inner.ToolTurn(ctx, req, onText)
	if err != nil {
		recordError(span, err)
		return nil, err
	}
	cached := res.Usage.CacheReadTokens
	prompt := res.Usage.InputTokens + cached + res.Usage.CacheWriteTokens
	RecordToolTurn(res.Model, prompt, res.Usage.OutputTokens, cached)
	span.SetAttributes(
		attribute.String("gen_ai.response.model", res.Model),
		attribute.Int64("gen_ai.usage.input_tokens", prompt),
		attribute.Int64("gen_ai.usage.output_tokens", res.Usage.OutputTokens),
	)
	span.SetStatus(codes.Ok, "")
	return res, nil
}

// NewAnthropicClient builds Anthropic alone, with no chain and no tools, metered
// and traced like the chain's own link. It is for a path that sends personal data
// and has to stay on Anthropic. It returns nil, with no error, when Anthropic is
// not switched on, so a caller cannot end up on another provider by accident.
func NewAnthropicClient(cfg config.AIConfig, logger *slog.Logger) (generativeAI.ChatClient, error) {
	link, err := newAnthropicEntry(cfg, logger)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, nil //nolint:nilnil // no link is a normal configuration
	}
	return link.client, nil
}
