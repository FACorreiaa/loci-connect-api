// Package apierr maps domain/sentinel errors to ConnectRPC error codes so
// handlers return meaningful statuses (NotFound, InvalidArgument, ...) instead
// of a blanket Internal, and clients can branch on the code.
package apierr

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// ToConnect converts err to a *connect.Error with an appropriate code. nil
// returns nil. Errors that are already *connect.Error pass through unchanged.
func ToConnect(err error) error {
	if err == nil {
		return nil
	}
	if ce := new(connect.Error); errors.As(err, &ce) {
		return err
	}

	switch {
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	case errors.Is(err, locitypes.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, locitypes.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, locitypes.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, locitypes.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, locitypes.ErrBadRequest):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
