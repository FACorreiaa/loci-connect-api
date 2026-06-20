package interceptors

import (
	"context"

	"connectrpc.com/connect"
)

// IPRateLimitInterceptor enforces per-client-IP rate limits.
type IPRateLimitInterceptor struct {
	store *keyedLimiterStore
}

// NewIPRateLimitInterceptor creates a per-IP rate limiting interceptor.
// Returns nil when perSecond, burst, or maxEntries are non-positive (disabled).
func NewIPRateLimitInterceptor(perSecond, burst, maxEntries int) *IPRateLimitInterceptor {
	store := newKeyedLimiterStore(perSecond, burst, maxEntries)
	if store == nil {
		return nil
	}
	return &IPRateLimitInterceptor{store: store}
}

func (i *IPRateLimitInterceptor) check(ctx context.Context, req connect.AnyRequest) error {
	if i == nil || i.store == nil {
		return nil
	}
	key := clientIPFromUnary(ctx, req)
	if key == "" {
		return nil
	}
	allowed, retryAfter := allowWithRetry(i.store.limiterFor(key))
	if allowed {
		return nil
	}
	return newRateLimitError(retryAfter)
}

func (i *IPRateLimitInterceptor) checkStream(conn connect.StreamingHandlerConn) error {
	if i == nil || i.store == nil {
		return nil
	}
	key := clientIPFromStream(conn)
	if key == "" {
		return nil
	}
	allowed, retryAfter := allowWithRetry(i.store.limiterFor(key))
	if allowed {
		return nil
	}
	return newRateLimitError(retryAfter)
}

// WrapUnary implements connect.Interceptor.
func (i *IPRateLimitInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.check(ctx, req); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *IPRateLimitInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *IPRateLimitInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := i.checkStream(conn); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// UserRateLimitInterceptor enforces per-authenticated-user rate limits.
// Skips requests without a user_id in context (e.g. public procedures).
type UserRateLimitInterceptor struct {
	store *keyedLimiterStore
}

// NewUserRateLimitInterceptor creates a per-user rate limiting interceptor.
// Returns nil when perSecond, burst, or maxEntries are non-positive (disabled).
func NewUserRateLimitInterceptor(perSecond, burst, maxEntries int) *UserRateLimitInterceptor {
	store := newKeyedLimiterStore(perSecond, burst, maxEntries)
	if store == nil {
		return nil
	}
	return &UserRateLimitInterceptor{store: store}
}

func (i *UserRateLimitInterceptor) check(ctx context.Context) error {
	if i == nil || i.store == nil {
		return nil
	}
	key := userIDFromContext(ctx)
	if key == "" {
		return nil
	}
	allowed, retryAfter := allowWithRetry(i.store.limiterFor(key))
	if allowed {
		return nil
	}
	return newRateLimitError(retryAfter)
}

// WrapUnary implements connect.Interceptor.
func (i *UserRateLimitInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.check(ctx); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *UserRateLimitInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *UserRateLimitInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := i.check(ctx); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}
