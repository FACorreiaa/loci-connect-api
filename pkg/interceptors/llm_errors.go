package interceptors

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// LLMErrorInterceptor translates typed LLM provider errors from the service
// layer into stable Connect codes so clients can distinguish "provider is
// throttling us, retry later" from generic internal failures.
type LLMErrorInterceptor struct{}

var _ connect.Interceptor = (*LLMErrorInterceptor)(nil)

// NewLLMErrorInterceptor creates an LLM error mapping interceptor.
func NewLLMErrorInterceptor() *LLMErrorInterceptor {
	return &LLMErrorInterceptor{}
}

// WrapUnary implements connect.Interceptor.
func (i *LLMErrorInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		resp, err := next(ctx, req)
		return resp, mapLLMError(err)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *LLMErrorInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *LLMErrorInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return mapLLMError(next(ctx, conn))
	}
}

func mapLLMError(err error) error {
	if err == nil {
		return nil
	}
	// Already a Connect error: leave whatever the handler chose intact.
	var cerr *connect.Error
	if errors.As(err, &cerr) {
		return err
	}
	switch {
	case errors.Is(err, llmerrors.ErrRateLimited):
		return connect.NewError(connect.CodeUnavailable, errors.New("AI provider is throttling requests, please retry shortly"))
	case errors.Is(err, llmerrors.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("AI provider is temporarily unavailable, please retry shortly"))
	default:
		return err
	}
}
