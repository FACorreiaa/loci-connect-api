package ai

import (
	"context"
	"encoding/json"
	"iter"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
)

// tracerName identifies this package's spans to OpenTelemetry.
const tracerName = "github.com/FACorreiaa/loci-connect-api/pkg/ai"

// tracing wraps a ChatClient with the gen_ai.* spans PostHog's AI
// Observability OTel span processor (wired up in pkg/observability) forwards
// as $ai_generation events. Go has no provider instrumentation library for
// this, so the spans are hand-authored here, at the one place every
// provider's calls already pass through.
type tracing struct {
	inner    generativeAI.ChatClient
	provider string
}

func newTracing(inner generativeAI.ChatClient, provider string) generativeAI.ChatClient {
	if inner == nil {
		return inner
	}
	return &tracing{inner: inner, provider: provider}
}

func (t *tracing) Generate(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	ctx, span := t.start(ctx, prompt, cfg)
	defer span.End()
	resp, err := t.inner.Generate(ctx, prompt, cfg)
	t.finish(span, resp, err)
	return resp, err
}

func (t *tracing) GenerateText(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig) (string, error) {
	ctx, span := t.start(ctx, prompt, cfg)
	defer span.End()
	text, err := t.inner.GenerateText(ctx, prompt, cfg)
	if err != nil {
		recordError(span, err)
		return text, err
	}
	// The SDK discards the response envelope here, so there is no usage to
	// report, same limitation pkg/ai/metered.go has for this method.
	span.SetAttributes(attribute.String("gen_ai.output.messages", messagesJSON("assistant", text)))
	span.SetStatus(codes.Ok, "")
	return text, nil
}

// GenerateStream ends the span once the caller finishes with the sequence, so
// a caller who abandons the stream still gets a span covering what was
// produced before they left.
func (t *tracing) GenerateStream(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	ctx, span := t.start(ctx, prompt, cfg)
	seq, err := t.inner.GenerateStream(ctx, prompt, cfg)
	if err != nil {
		recordError(span, err)
		span.End()
		return seq, err
	}
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		defer span.End()
		var (
			last     *genai.GenerateContentResponse
			lastErr  error
			sawError bool
		)
		for resp, rerr := range seq {
			if rerr != nil {
				sawError, lastErr = true, rerr
			}
			if resp != nil {
				last = resp
			}
			if !yield(resp, rerr) {
				return
			}
		}
		if sawError {
			recordError(span, lastErr)
			return
		}
		t.finish(span, last, nil)
	}, nil
}

func (t *tracing) StartChatSession(ctx context.Context, cfg *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return t.inner.StartChatSession(ctx, cfg)
}

func (t *tracing) Model() string { return t.inner.Model() }
func (t *tracing) Close() error  { return t.inner.Close() }

func (t *tracing) start(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig) (context.Context, trace.Span) {
	model := t.inner.Model()
	ctx, span := otel.Tracer(tracerName).Start(ctx, "chat "+model)
	span.SetAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.provider.name", t.provider),
		attribute.String("gen_ai.request.model", model),
		attribute.String("gen_ai.input.messages", inputMessagesJSON(cfg, prompt)),
	)
	return ctx, span
}

func (t *tracing) finish(span trace.Span, resp *genai.GenerateContentResponse, err error) {
	if err != nil {
		recordError(span, err)
		return
	}
	if resp == nil {
		span.SetStatus(codes.Ok, "")
		return
	}
	span.SetAttributes(attribute.String("gen_ai.output.messages", messagesJSON("assistant", resp.Text())))
	if u := resp.UsageMetadata; u != nil {
		span.SetAttributes(
			attribute.Int("gen_ai.usage.input_tokens", int(u.PromptTokenCount)),
			attribute.Int("gen_ai.usage.output_tokens", int(u.CandidatesTokenCount)),
		)
	}
	span.SetStatus(codes.Ok, "")
}

func recordError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

type tracedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func inputMessagesJSON(cfg *genai.GenerateContentConfig, prompt string) string {
	messages := make([]tracedMessage, 0, 2)
	if system := systemInstructionText(cfg); system != "" {
		messages = append(messages, tracedMessage{Role: "system", Content: system})
	}
	messages = append(messages, tracedMessage{Role: "user", Content: prompt})
	return messagesJSONSlice(messages)
}

func messagesJSON(role, content string) string {
	return messagesJSONSlice([]tracedMessage{{Role: role, Content: content}})
}

func messagesJSONSlice(messages []tracedMessage) string {
	b, err := json.Marshal(messages)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func systemInstructionText(cfg *genai.GenerateContentConfig) string {
	if cfg == nil || cfg.SystemInstruction == nil {
		return ""
	}
	var out string
	for _, part := range cfg.SystemInstruction.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += part.Text
	}
	return out
}
