package runs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat/chatconnect"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

const CapMessage = "You have 3 searches running — wait for one to finish"

// CapInterceptor holds each person to MaxConcurrent generations. It sits
// after auth (it needs the user id) and before the quota interceptor, so a
// refused search costs no quota. It reads the first request itself to tell
// a resume (never capped) from a new search, then hands that same message
// to the handler.
type CapInterceptor struct {
	store  Store
	logger *slog.Logger
}

func NewCapInterceptor(store Store, logger *slog.Logger) *CapInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return &CapInterceptor{store: store, logger: logger}
}

func (i *CapInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc { return next }

func (i *CapInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *CapInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if conn.Spec().Procedure != chatconnect.ChatServiceStreamChatProcedure {
			return next(ctx, conn)
		}
		userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
		userID, err := uuid.Parse(userIDStr)
		if !ok || err != nil {
			return next(ctx, conn) // the handler answers Unauthenticated
		}

		first := &chatv1.ChatRequest{}
		if err := conn.Receive(first); err != nil {
			return err
		}
		peeked := &peekedConn{StreamingHandlerConn: conn, first: first}
		if first.GetResumeToken() != "" {
			return next(ctx, peeked)
		}

		runID, err := i.store.Reserve(ctx, userID)
		if errors.Is(err, ErrAtCapacity) {
			// CapMessage is shown to the person verbatim, not an internal Go
			// error string, hence the capitalized sentence.
			return connect.NewError(connect.CodeResourceExhausted, errors.New(CapMessage)) //nolint:staticcheck // ST1005: user-facing copy
		}
		if err != nil {
			// Losing the record must not lose the search: log and serve it
			// uncapped rather than fail someone's request on a DB hiccup.
			i.logger.Error("run reservation failed; serving uncapped", "error", err)
			return next(ctx, peeked)
		}

		res := &Reservation{RunID: runID}
		defer func() {
			if !res.claimed.Load() {
				if rErr := i.store.Release(context.WithoutCancel(ctx), runID); rErr != nil {
					i.logger.Warn("release unclaimed run", "run_id", runID, "error", rErr)
				}
			}
		}()
		return next(withReservation(ctx, res), peeked)
	}
}

// peekedConn returns the already-read first request, then defers to the
// real connection.
type peekedConn struct {
	connect.StreamingHandlerConn
	first *chatv1.ChatRequest
	used  bool
}

func (c *peekedConn) Receive(msg any) error {
	if c.used {
		return c.StreamingHandlerConn.Receive(msg)
	}
	c.used = true
	dst, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("runs: unexpected request type %T", msg)
	}
	proto.Merge(dst, c.first)
	return nil
}
