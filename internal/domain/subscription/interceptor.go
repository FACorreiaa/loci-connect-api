package subscription

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"github.com/google/uuid"
)

type RateLimitInterceptor struct {
	service Service
}

func NewRateLimitInterceptor(service Service) *RateLimitInterceptor {
	return &RateLimitInterceptor{service: service}
}

func (i *RateLimitInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		proc := req.Spec().Procedure
		if isChatProcedure(proc) {
			if err := i.checkLimit(ctx); err != nil {
				return nil, err
			}
			// Record usage after checking limit.
			// Ideally we record only on success, but to prevent spam/abuse we might want to record attempt?
			// The requirement said "5 requests per day".
			// If we record *after*, they can parallel spam.
			// Let's record *before* (token bucket style consumption).
			// If request fails, they lost a token. This is safer for rate limiting.
			userID, _ := extractUser(ctx)
			_ = i.service.RecordUsage(ctx, userID)
		}
		return next(ctx, req)
	})
}

func (i *RateLimitInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *RateLimitInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return connect.StreamingHandlerFunc(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		proc := conn.Spec().Procedure
		if isChatProcedure(proc) {
			if err := i.checkLimit(ctx); err != nil {
				return err
			}
			userID, _ := extractUser(ctx)
			_ = i.service.RecordUsage(ctx, userID)
		}
		return next(ctx, conn)
	})
}

func (i *RateLimitInterceptor) checkLimit(ctx context.Context) error {
	userID, err := extractUser(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	email := extractEmail(ctx) // Claims handling needed for this

	if err := i.service.CheckRateLimit(ctx, userID, email); err != nil {
		if errors.Is(err, ErrQuotaExceeded) {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

func isChatProcedure(proc string) bool {
	return strings.Contains(proc, "StartChat") ||
		strings.Contains(proc, "ContinueChat") ||
		strings.Contains(proc, "ContinueSessionStreamed")
}

func extractUser(ctx context.Context) (uuid.UUID, error) {
	// Use the helper from pkg/interceptors
	uidStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok {
		return uuid.Nil, errors.New("no user id in context")
	}
	return uuid.Parse(uidStr)
}

func extractEmail(ctx context.Context) string {
	claims, err := interceptors.GetClaimsFromContext(ctx)
	if err != nil || claims == nil {
		return ""
	}
	return claims.Email
}
