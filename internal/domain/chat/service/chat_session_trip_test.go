package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
)

type fakeSessionTrips struct {
	trip  *trip.Trip
	err   error
	calls int
}

func (f *fakeSessionTrips) LatestForSession(context.Context, uuid.UUID, uuid.UUID) (*trip.Trip, error) {
	f.calls++
	return f.trip, f.err
}

func TestSessionTripToKeep(t *testing.T) {
	session, tripID := uuid.New(), uuid.New()
	lisbon := &trip.Trip{ID: tripID, CityName: "Lisbon"}
	resumed := common.ChatContext{UserID: uuid.New(), SessionID: session, RequestedSessionID: session, CityName: "lisbon "}
	svc := func(f *fakeSessionTrips) *ServiceImpl {
		l := &ServiceImpl{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		if f != nil {
			l.SetSessionTrips(f)
		}
		return l
	}

	t.Run("a follow-up in a session with a trip keeps that trip", func(t *testing.T) {
		require.Equal(t, tripID, svc(&fakeSessionTrips{trip: lisbon}).sessionTripToKeep(context.Background(), resumed))
	})

	t.Run("a first generation has nothing to keep and is not looked up", func(t *testing.T) {
		f := &fakeSessionTrips{trip: lisbon}
		fresh := resumed
		fresh.RequestedSessionID = uuid.Nil
		require.Equal(t, uuid.Nil, svc(f).sessionTripToKeep(context.Background(), fresh))
		require.Zero(t, f.calls)
	})

	t.Run("a requested session that could not be resumed is a new session", func(t *testing.T) {
		f := &fakeSessionTrips{trip: lisbon}
		other := resumed
		other.SessionID = uuid.New()
		require.Equal(t, uuid.Nil, svc(f).sessionTripToKeep(context.Background(), other))
		require.Zero(t, f.calls)
	})

	t.Run("another city in the same session still gets its own trip", func(t *testing.T) {
		warsaw := resumed
		warsaw.CityName = "Warsaw"
		require.Equal(t, uuid.Nil, svc(&fakeSessionTrips{trip: lisbon}).sessionTripToKeep(context.Background(), warsaw))
	})

	t.Run("no trip yet, or a failed lookup, saves as before", func(t *testing.T) {
		require.Equal(t, uuid.Nil, svc(&fakeSessionTrips{err: trip.ErrNotFound}).sessionTripToKeep(context.Background(), resumed))
		require.Equal(t, uuid.Nil, svc(&fakeSessionTrips{err: errors.New("db down")}).sessionTripToKeep(context.Background(), resumed))
	})

	t.Run("a turn bound to a trip, or told not to save, is left alone", func(t *testing.T) {
		f := &fakeSessionTrips{trip: lisbon}
		bound := resumed
		bound.TripID = uuid.New()
		require.Equal(t, uuid.Nil, svc(f).sessionTripToKeep(context.Background(), bound))
		quiet := resumed
		quiet.SuppressTripSave = true
		require.Equal(t, uuid.Nil, svc(f).sessionTripToKeep(context.Background(), quiet))
		require.Zero(t, f.calls)
	})

	t.Run("without the lookup wired nothing changes", func(t *testing.T) {
		require.Equal(t, uuid.Nil, svc(nil).sessionTripToKeep(context.Background(), resumed))
	})
}
