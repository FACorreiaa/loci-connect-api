package interceptors

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
)

// NewLoggingInterceptor creates a new logging interceptor
func NewLoggingInterceptor(logger *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()

			logger.Info("RPC started", appendLoggerFields(ctx,
				"procedure", req.Spec().Procedure,
				"peer", req.Peer().Addr,
			)...)

			resp, err := next(ctx, req)

			duration := time.Since(start)

			if err != nil {
				logger.Error("RPC failed", appendLoggerFields(ctx,
					"procedure", req.Spec().Procedure,
					"duration", duration.String(),
					"error", err,
				)...)
			} else {
				logger.Info("RPC completed", appendLoggerFields(ctx,
					"procedure", req.Spec().Procedure,
					"duration", duration.String(),
				)...)
			}

			return resp, err
		}
	}
}

func appendLoggerFields(ctx context.Context, base ...any) []any {
	if requestID, ok := RequestIDFromContext(ctx); ok && requestID != "" {
		base = append(base, "request_id", requestID)
	}
	return base
}
