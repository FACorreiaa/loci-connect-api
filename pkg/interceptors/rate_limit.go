package interceptors

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"
)

// RateLimitInterceptor enforces a global rate limit for incoming RPCs.
type RateLimitInterceptor struct {
	limiter *rate.Limiter
}

// NewRateLimitInterceptor creates a rate limiting interceptor.
func NewRateLimitInterceptor(limiter *rate.Limiter) *RateLimitInterceptor {
	return &RateLimitInterceptor{limiter: limiter}
}

// WrapUnary implements connect.Interceptor.
func (i *RateLimitInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i == nil || i.limiter == nil || i.limiter.Allow() {
			return next(ctx, req)
		}
		return nil, connect.NewError(
			connect.CodeResourceExhausted,
			errors.New("rate limit exceeded"),
		)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *RateLimitInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *RateLimitInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if i == nil || i.limiter == nil || i.limiter.Allow() {
			return next(ctx, conn)
		}
		return connect.NewError(
			connect.CodeResourceExhausted,
			errors.New("rate limit exceeded"),
		)
	}
}
