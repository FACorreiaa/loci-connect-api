package observability

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type (
	aiSessionKey    struct{}
	aiDistinctIDKey struct{}
)

// WithAISession returns a context whose spans carry $ai_session_id, so
// PostHog's AI Observability groups every LLM call of one chat turn into the
// conversation's session. sessionID is expected to already satisfy PostHog's
// charset for the property (letters, numbers, - _ ~ . @ ( ) ! ' : |); a UUID
// string does.
func WithAISession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, aiSessionKey{}, sessionID)
}

// WithAIDistinctID returns a context whose spans carry posthog.distinct_id,
// attributing the generations made under ctx to that user instead of leaving
// them anonymous.
func WithAIDistinctID(ctx context.Context, distinctID string) context.Context {
	if distinctID == "" {
		return ctx
	}
	return context.WithValue(ctx, aiDistinctIDKey{}, distinctID)
}

// AIContextSpanProcessor copies the AI session and distinct id carried on a
// span's context onto the span itself as it starts.
//
// PostHog's AI span filter reads span attributes only, never resource
// attributes, so a value set once on the TracerProvider's resource would
// never reach the $ai_session_id / posthog.distinct_id of a per-user,
// per-conversation span. Register this next to the PostHog span processor.
type AIContextSpanProcessor struct{}

var _ sdktrace.SpanProcessor = AIContextSpanProcessor{}

func (AIContextSpanProcessor) OnStart(parent context.Context, span sdktrace.ReadWriteSpan) {
	if sessionID, ok := parent.Value(aiSessionKey{}).(string); ok {
		span.SetAttributes(attribute.String("$ai_session_id", sessionID))
	}
	if distinctID, ok := parent.Value(aiDistinctIDKey{}).(string); ok {
		span.SetAttributes(attribute.String("posthog.distinct_id", distinctID))
	}
}

func (AIContextSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (AIContextSpanProcessor) Shutdown(context.Context) error   { return nil }
func (AIContextSpanProcessor) ForceFlush(context.Context) error { return nil }
