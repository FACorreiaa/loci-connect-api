package interceptors

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"connectrpc.com/connect"
)

// RecoveryInterceptor recovers panics in unary and streaming RPC handlers.
type RecoveryInterceptor struct {
	logger *slog.Logger
}

var _ connect.Interceptor = (*RecoveryInterceptor)(nil)

// NewRecoveryInterceptor creates a recovery interceptor.
func NewRecoveryInterceptor(logger *slog.Logger) *RecoveryInterceptor {
	return &RecoveryInterceptor{logger: logger}
}

// WrapUnary implements connect.Interceptor.
func (i *RecoveryInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
		defer func() {
			if r := recover(); r != nil {
				i.logPanic(req.Spec().Procedure, r)
				err = connect.NewError(
					connect.CodeInternal,
					fmt.Errorf("internal server error: %v", r),
				)
			}
		}()
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *RecoveryInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *RecoveryInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) (err error) {
		defer func() {
			if r := recover(); r != nil {
				i.logPanic(conn.Spec().Procedure, r)
				err = connect.NewError(
					connect.CodeInternal,
					fmt.Errorf("internal server error: %v", r),
				)
			}
		}()
		return next(ctx, conn)
	}
}

func (i *RecoveryInterceptor) logPanic(procedure string, recovered any) {
	if i.logger == nil {
		return
	}
	i.logger.Error("panic recovered",
		"procedure", procedure,
		"panic", recovered,
		"stack", string(debug.Stack()),
	)
}
