package handler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	itineraryv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/itinerary"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakeItineraries map[uuid.UUID]*locitypes.UserSavedItinerary

func (f fakeItineraries) GetItinerary(_ context.Context, userID, id uuid.UUID) (*locitypes.UserSavedItinerary, error) {
	if it, ok := f[id]; ok && it.UserID == userID {
		return it, nil
	}
	return nil, fmt.Errorf("no itinerary found: %w", pgx.ErrNoRows)
}

func TestGetItinerary(t *testing.T) {
	owner := uuid.New()
	id := uuid.New()
	session := uuid.New()
	h := NewItineraryHandler(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithItineraries(fakeItineraries{id: {ID: id, UserID: owner, Title: "Three days in Porto", SessionID: &session}})
	as := func(u uuid.UUID) context.Context {
		return context.WithValue(context.Background(), interceptors.UserIDKey, u.String())
	}

	t.Run("owner reads it", func(t *testing.T) {
		res, err := h.GetItinerary(as(owner), connect.NewRequest(&itineraryv1.GetItineraryRequest{ItineraryId: id.String()}))
		require.NoError(t, err)
		assert.Equal(t, "Three days in Porto", res.Msg.Itinerary.Title)
		assert.Equal(t, session.String(), res.Msg.Itinerary.GetSessionId())
	})

	t.Run("someone else's is not found", func(t *testing.T) {
		_, err := h.GetItinerary(as(uuid.New()), connect.NewRequest(&itineraryv1.GetItineraryRequest{ItineraryId: id.String()}))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("bad id", func(t *testing.T) {
		_, err := h.GetItinerary(as(owner), connect.NewRequest(&itineraryv1.GetItineraryRequest{ItineraryId: "nope"}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("signed out", func(t *testing.T) {
		_, err := h.GetItinerary(context.Background(), connect.NewRequest(&itineraryv1.GetItineraryRequest{ItineraryId: id.String()}))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
