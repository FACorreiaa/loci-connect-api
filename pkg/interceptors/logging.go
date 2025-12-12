package interceptors

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// NewLoggingInterceptor creates a new logging interceptor with payload size tracking
func NewLoggingInterceptor(logger *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()

			// Calculate request payload size
			requestSize := 0
			if msg, ok := req.Any().(proto.Message); ok {
				requestSize = proto.Size(msg)
			}

			logger.Info("RPC started", appendLoggerFields(ctx,
				"procedure", req.Spec().Procedure,
				"peer", req.Peer().Addr,
				"request_size_bytes", requestSize,
			)...)

			resp, err := next(ctx, req)

			duration := time.Since(start)

			// Calculate response payload size safely
			responseSize := 0
			if resp != nil {
				// Use defer/recover because resp.Any() can panic on nil inner message
				func() {
					defer func() {
						if r := recover(); r != nil {
							// resp.Any() panicked - response has nil inner message
							responseSize = 0
						}
					}()
					if anyResp := resp.Any(); anyResp != nil {
						if msg, ok := anyResp.(proto.Message); ok {
							responseSize = proto.Size(msg)
						}
					}
				}()
			}

			if err != nil {
				logger.Error("RPC failed", appendLoggerFields(ctx,
					"procedure", req.Spec().Procedure,
					"duration", duration.String(),
					"duration_ms", duration.Milliseconds(),
					"request_size_bytes", requestSize,
					"response_size_bytes", responseSize,
					"error", err,
				)...)
			} else {
				logger.Info("RPC completed", appendLoggerFields(ctx,
					"procedure", req.Spec().Procedure,
					"duration", duration.String(),
					"duration_ms", duration.Milliseconds(),
					"request_size_bytes", requestSize,
					"response_size_bytes", responseSize,
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
