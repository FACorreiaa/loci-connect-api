package subscription

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/chat/chatconnect"
	"github.com/google/uuid"
)

// QuotaReasonHeader is set on ResourceExhausted responses so the client can
// distinguish a free-tier denial (show upgrade CTA) from a Pro fair-use
// denial (show retry-tomorrow copy, never an upgrade CTA).
const (
	QuotaReasonHeader    = "X-Loci-Quota-Reason"
	QuotaReasonFreeDaily = "free_daily_limit"
	QuotaReasonFairUse   = "fair_use"
)

// meteredProcedures are the LLM-backed RPCs that consume daily quota.
// Cheap reads (sessions, bookmarks, POI search) stay unmetered; POI semantic
// search only makes an embedding call, which is negligible next to chat
// generation. Uses generated procedure constants so renames fail the build.
var meteredProcedures = map[string]struct{}{
	chatconnect.ChatServiceStartChatProcedure:    {},
	chatconnect.ChatServiceContinueChatProcedure: {},
	chatconnect.ChatServiceStreamChatProcedure:   {},
}

func isMeteredProcedure(proc string) bool {
	_, ok := meteredProcedures[proc]
	return ok
}

type RateLimitInterceptor struct {
	service Service
}

func NewRateLimitInterceptor(service Service) *RateLimitInterceptor {
	return &RateLimitInterceptor{service: service}
}

func (i *RateLimitInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if isMeteredProcedure(req.Spec().Procedure) {
			if err := i.consumeQuota(ctx); err != nil {
				return nil, err
			}
		}
		return next(ctx, req)
	})
}

func (i *RateLimitInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *RateLimitInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return connect.StreamingHandlerFunc(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if isMeteredProcedure(conn.Spec().Procedure) {
			if err := i.consumeQuota(ctx); err != nil {
				return err
			}
		}
		return next(ctx, conn)
	})
}

// consumeQuota spends one daily request up front (token-bucket style: a
// failed downstream call still costs a token, which keeps parallel spam from
// outrunning the counter) and maps service errors to Connect codes.
func (i *RateLimitInterceptor) consumeQuota(ctx context.Context) error {
	userID, err := extractUser(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}

	err = i.service.ConsumeQuota(ctx, userID, extractEmail(ctx))
	if err == nil {
		return nil
	}

	var quotaErr *QuotaExceededError
	if errors.As(err, &quotaErr) {
		cerr := connect.NewError(connect.CodeResourceExhausted, err)
		if isProPlan(quotaErr.Plan) {
			cerr.Meta().Set(QuotaReasonHeader, QuotaReasonFairUse)
		} else {
			cerr.Meta().Set(QuotaReasonHeader, QuotaReasonFreeDaily)
		}
		return cerr
	}
	return connect.NewError(connect.CodeInternal, err)
}

func extractUser(ctx context.Context) (uuid.UUID, error) {
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
