package interceptors

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

type requestIDKey struct{}

// RequestIDFromContext retrieves the request ID from context if present.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(requestIDKey{}).(string)
	return value, ok
}

// RequestIDInterceptor injects/propagates a request identifier for each RPC.
type RequestIDInterceptor struct {
	headerName string
}

// NewRequestIDInterceptor creates a request-ID interceptor.
func NewRequestIDInterceptor(headerName string) *RequestIDInterceptor {
	if strings.TrimSpace(headerName) == "" {
		headerName = "X-Request-ID"
	}
	return &RequestIDInterceptor{
		headerName: headerName,
	}
}

// WrapUnary implements connect.Interceptor.
func (i *RequestIDInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		requestID, ctxWithID := i.ensureRequestID(ctx, req.Header())
		resp, err := next(ctxWithID, req)
		// Safely set response header - resp might be nil on error
		if resp != nil && requestID != "" {
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Silently handle nil pointer in response interface
					}
				}()
				if header := resp.Header(); header != nil {
					header.Set(i.headerName, requestID)
				}
			}()
		}
		return resp, err
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *RequestIDInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *RequestIDInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		requestID, ctxWithID := i.ensureRequestID(ctx, conn.RequestHeader())
		if requestID != "" {
			conn.ResponseHeader().Set(i.headerName, requestID)
		}
		return next(ctxWithID, conn)
	}
}

func (i *RequestIDInterceptor) ensureRequestID(ctx context.Context, header http.Header) (string, context.Context) {
	requestID := ""
	if header != nil {
		requestID = header.Get(i.headerName)
	}
	if requestID == "" {
		requestID = uuid.New().String()
	}
	return requestID, context.WithValue(ctx, requestIDKey{}, requestID)
}
