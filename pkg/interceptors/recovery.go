package interceptors

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"connectrpc.com/connect"
)

// NewRecoveryInterceptor creates a new recovery interceptor to handle panics
func NewRecoveryInterceptor(logger *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("panic recovered",
						"procedure", req.Spec().Procedure,
						"panic", r,
						"stack", string(debug.Stack()),
					)

					err = connect.NewError(
						connect.CodeInternal,
						fmt.Errorf("internal server error: %v", r),
					)
				}
			}()

			return next(ctx, req)
		}
	}
}
