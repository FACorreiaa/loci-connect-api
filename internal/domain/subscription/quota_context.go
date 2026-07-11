package subscription

import "context"

type quotaConsumerKey struct{}

// QuotaConsumerFunc spends one quota unit for the request's principal.
type QuotaConsumerFunc func(ctx context.Context) error

// WithQuotaConsumer arms the context so that downstream LLM-generation call
// sites meter usage via ConsumeQuotaFromContext. Channels that meter at the
// interceptor instead (web chat RPCs) simply never arm the context, so their
// behavior is unchanged.
func WithQuotaConsumer(ctx context.Context, fn QuotaConsumerFunc) context.Context {
	return context.WithValue(ctx, quotaConsumerKey{}, fn)
}

// ConsumeQuotaFromContext spends one quota unit when the context carries a
// consumer, and is a no-op otherwise. Call it immediately before expensive
// LLM/embedding work so cache and database hits stay free.
func ConsumeQuotaFromContext(ctx context.Context) error {
	fn, ok := ctx.Value(quotaConsumerKey{}).(QuotaConsumerFunc)
	if !ok || fn == nil {
		return nil
	}
	return fn(ctx)
}
