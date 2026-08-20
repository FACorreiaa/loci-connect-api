package interceptors

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// LoggingInterceptor logs unary and streaming RPC lifecycle events.
type LoggingInterceptor struct {
	logger *slog.Logger
}

var _ connect.Interceptor = (*LoggingInterceptor)(nil)

// NewLoggingInterceptor creates a logging interceptor with payload size tracking.
func NewLoggingInterceptor(logger *slog.Logger) *LoggingInterceptor {
	return &LoggingInterceptor{logger: logger}
}

func (i *LoggingInterceptor) loggerOrDefault() *slog.Logger {
	if i.logger != nil {
		return i.logger
	}
	return slog.Default()
}

// WrapUnary implements connect.Interceptor.
func (i *LoggingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		logger := i.loggerOrDefault()
		start := time.Now()
		requestSize := protoSize(req.Any())

		logger.Info("RPC started", appendLoggerFields(ctx,
			"procedure", req.Spec().Procedure,
			"peer", req.Peer().Addr,
			"request_size_bytes", requestSize,
		)...)

		resp, err := next(ctx, req)
		i.logUnaryComplete(ctx, logger, req.Spec().Procedure, start, requestSize, resp, err)
		return resp, err
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *LoggingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *LoggingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		start := time.Now()
		procedure := conn.Spec().Procedure

		logger := i.loggerOrDefault()
		logger.Info("RPC stream started", appendLoggerFields(ctx,
			"procedure", procedure,
			"peer", conn.Peer().Addr,
		)...)

		err := next(ctx, conn)
		duration := time.Since(start)
		if err != nil {
			logger.Error("RPC stream failed", appendLoggerFields(ctx,
				"procedure", procedure,
				"duration", duration.String(),
				"duration_ms", duration.Milliseconds(),
				"error", err,
			)...)
		} else {
			logger.Info("RPC stream completed", appendLoggerFields(ctx,
				"procedure", procedure,
				"duration", duration.String(),
				"duration_ms", duration.Milliseconds(),
			)...)
		}
		return err
	}
}

func (i *LoggingInterceptor) logUnaryComplete(
	ctx context.Context,
	logger *slog.Logger,
	procedure string,
	start time.Time,
	requestSize int,
	resp connect.AnyResponse,
	err error,
) {
	duration := time.Since(start)
	responseSize := 0
	if resp != nil {
		responseSize = protoSize(resp.Any())
	}
	if err != nil {
		logger.Error("RPC failed", appendLoggerFields(ctx,
			"procedure", procedure,
			"duration", duration.String(),
			"duration_ms", duration.Milliseconds(),
			"request_size_bytes", requestSize,
			"response_size_bytes", responseSize,
			"error", err,
		)...)
		return
	}
	logger.Info("RPC completed", appendLoggerFields(ctx,
		"procedure", procedure,
		"duration", duration.String(),
		"duration_ms", duration.Milliseconds(),
		"request_size_bytes", requestSize,
		"response_size_bytes", responseSize,
	)...)
}

func protoSize(msg any) int {
	if msg == nil {
		return 0
	}
	if pb, ok := msg.(proto.Message); ok {
		return proto.Size(pb)
	}
	return 0
}

func appendLoggerFields(ctx context.Context, base ...any) []any {
	if requestID, ok := RequestIDFromContext(ctx); ok && requestID != "" {
		base = append(base, "request_id", requestID)
	}
	return base
}
