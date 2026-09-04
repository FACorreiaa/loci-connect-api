package mcp

import (
	"context"
	"net/http"
	"time"
)

// withTimeout gives every MCP request a deadline.
//
// The endpoint is mounted outside the Connect interceptor chain, so it inherits
// neither CHAT_RPC_TIMEOUT_SEC nor the default RPC deadline, and the server's
// WriteTimeout is deliberately 0 so streaming responses are not cut off. A tool
// that hangs would otherwise hold its goroutine and connection for as long as
// the client is willing to wait.
//
// The deadline is applied to the request context and the handler runs inline.
// It is deliberately not enforced by running the handler on its own goroutine
// and abandoning it: net/http finishes the request as soon as ServeHTTP
// returns, so an abandoned tool goes on writing to a response the server has
// already completed, which is a genuine data race. Buffering the body instead,
// the way http.TimeoutHandler does, would defeat MCP's streaming transport.
//
// This bounds every tool that honours its context, which is all of them: tool
// calls reach the service layer through ctx and stop with it. A tool that
// ignored its context would not be interrupted, and that is a bug to fix in the
// tool rather than to paper over here.
//
// A non-positive timeout disables the middleware.
func withTimeout(timeout time.Duration, next http.Handler) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
