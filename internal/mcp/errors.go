package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// toolError translates service-layer failures into messages an MCP client
// (and the human behind it) can act on. MCP has no header channel, so quota
// and throttling details must travel in the error text itself.
func toolError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, subscription.ErrQuotaExceeded):
		var qerr *subscription.QuotaExceededError
		if errors.As(err, &qerr) && qerr.Plan == subscription.PlanFree {
			return errors.New("daily free quota exhausted; it resets at midnight UTC — upgrade to Pro for a higher limit")
		}
		return errors.New("daily fair-use quota exhausted; it resets at midnight UTC")
	case errors.Is(err, llmerrors.ErrRateLimited):
		return errors.New("the AI provider is throttling requests; retry in a few seconds")
	case errors.Is(err, llmerrors.ErrUnavailable):
		return errors.New("the AI provider is temporarily unavailable; retry in a few seconds")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("the request timed out; try a smaller radius or a more specific query")
	default:
		// Generic wrap: don't leak internals, keep a stable prefix for support.
		return fmt.Errorf("loci request failed: %v", err)
	}
}
