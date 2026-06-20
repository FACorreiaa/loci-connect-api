package interceptors

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
)

// TimeoutConfig holds server-side RPC deadline defaults.
type TimeoutConfig struct {
	Default   time.Duration
	Chat      time.Duration
	StreamMax time.Duration
}

// TimeoutInterceptor applies deadlines to unary RPCs and caps client timeouts on streams.
type TimeoutInterceptor struct {
	cfg TimeoutConfig
}

var _ connect.Interceptor = (*TimeoutInterceptor)(nil)

// NewTimeoutInterceptor creates a timeout interceptor.
func NewTimeoutInterceptor(cfg TimeoutConfig) *TimeoutInterceptor {
	return &TimeoutInterceptor{cfg: cfg}
}

// WrapUnary implements connect.Interceptor.
func (i *TimeoutInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		timeout, apply := i.resolveUnaryTimeout(req.Spec().Procedure, req.Header())
		if !apply {
			return next(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *TimeoutInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *TimeoutInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// Streaming RPCs keep long-running LLM work in domain code; only honor an
		// explicit client deadline when present, capped by StreamMax.
		if timeout, ok := parseClientTimeout(conn.RequestHeader()); ok && i.cfg.StreamMax > 0 {
			if timeout > i.cfg.StreamMax {
				timeout = i.cfg.StreamMax
			}
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return next(ctx, conn)
	}
}

func (i *TimeoutInterceptor) resolveUnaryTimeout(procedure string, header http.Header) (time.Duration, bool) {
	if timeout, ok := parseClientTimeout(header); ok {
		return timeout, true
	}
	if isChatUnaryProcedure(procedure) && i.cfg.Chat > 0 {
		return i.cfg.Chat, true
	}
	if i.cfg.Default > 0 {
		return i.cfg.Default, true
	}
	return 0, false
}

func isChatUnaryProcedure(procedure string) bool {
	return strings.Contains(procedure, "StartChat") ||
		strings.Contains(procedure, "ContinueChat")
}

func parseClientTimeout(header http.Header) (time.Duration, bool) {
	if header == nil {
		return 0, false
	}
	if raw := strings.TrimSpace(header.Get("Connect-Timeout-Ms")); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil || ms <= 0 {
			return 0, false
		}
		return time.Duration(ms) * time.Millisecond, true
	}
	if raw := strings.TrimSpace(header.Get("Grpc-Timeout")); raw != "" {
		return parseGRPCTimeout(raw)
	}
	return 0, false
}

// parseGRPCTimeout parses gRPC timeout values such as "10S" or "500m".
func parseGRPCTimeout(raw string) (time.Duration, bool) {
	if len(raw) < 2 {
		return 0, false
	}
	unit := raw[len(raw)-1]
	valueStr := raw[:len(raw)-1]
	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	switch unit {
	case 'n':
		return time.Duration(value), true
	case 'u':
		return time.Duration(value) * time.Microsecond, true
	case 'm':
		return time.Duration(value) * time.Millisecond, true
	case 'S':
		return time.Duration(value) * time.Second, true
	case 'M':
		return time.Duration(value) * time.Minute, true
	case 'H':
		return time.Duration(value) * time.Hour, true
	default:
		return 0, false
	}
}