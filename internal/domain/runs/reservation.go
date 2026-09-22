package runs

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"
)

// Reservation is a run row the cap interceptor inserted for this request.
// The handler claims it once it has a generation to attach it to; an
// unclaimed one is released when the request ends, so quota refusals and
// bad requests do not leave phantom running rows behind.
type Reservation struct {
	RunID   uuid.UUID
	claimed atomic.Bool
}

func (r *Reservation) Claim() { r.claimed.Store(true) }

type reservationKey struct{}

func withReservation(ctx context.Context, r *Reservation) context.Context {
	return context.WithValue(ctx, reservationKey{}, r)
}

func ReservationFrom(ctx context.Context) (*Reservation, bool) {
	r, ok := ctx.Value(reservationKey{}).(*Reservation)
	return r, ok
}
