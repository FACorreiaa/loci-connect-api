package handler

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

func TestMapEventToProto_ActionProposal(t *testing.T) {
	p := tripaction.Proposal{
		ID: uuid.New(), TripID: uuid.New(), Summary: "Set dates",
		Action:    tripaction.Action{Kind: tripaction.KindSetDates, StartDate: "2026-11-12", EndDate: "2026-11-17"},
		ExpiresAt: time.Now(),
	}
	resp, err := (&ChatHandler{}).mapEventToProto(context.Background(),
		locitypes.StreamEvent{Type: locitypes.EventTypeActionProposal, EventID: "e1", Data: p}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, chatv1.StreamEventType_STREAM_EVENT_TYPE_ACTION_PROPOSAL, resp.GetEventType())
	got := resp.GetActionProposal().GetProposal()
	require.Equal(t, p.ID.String(), got.GetId())
	require.Equal(t, "2026-11-12", got.GetAction().GetSetDates().GetStartDate())
}

type fakeTripActions struct{ err error }

func (f fakeTripActions) Apply(context.Context, uuid.UUID, uuid.UUID, *int, int64) (*trip.Trip, *locitypes.ConversationMessage, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return &trip.Trip{ID: uuid.New(), Title: "Lisbon", Version: 4}, &locitypes.ConversationMessage{Content: "Dates set"}, nil
}

func (f fakeTripActions) Dismiss(context.Context, uuid.UUID, uuid.UUID) error { return f.err }

func signedIn() context.Context {
	return context.WithValue(context.Background(), interceptors.UserIDKey, uuid.NewString())
}

func TestApplyTripAction(t *testing.T) {
	h := (&ChatHandler{}).WithTripActions(fakeTripActions{})
	res, err := h.ApplyTripAction(signedIn(), connect.NewRequest(&chatv1.ApplyTripActionRequest{ProposalId: uuid.NewString(), BaseVersion: 3}))
	require.NoError(t, err)
	require.EqualValues(t, 4, res.Msg.GetTrip().GetVersion())
	require.Equal(t, "Dates set", res.Msg.GetConfirmation().GetContent())
}

func TestTripActionErrorCodes(t *testing.T) {
	for err, code := range map[error]connect.Code{
		tripaction.ErrNotFound:   connect.CodeNotFound,
		trip.ErrNotFound:         connect.CodeNotFound,
		tripaction.ErrNotPending: connect.CodeFailedPrecondition,
		tripaction.ErrExpired:    connect.CodeFailedPrecondition,
		trip.ErrVersionConflict:  connect.CodeFailedPrecondition,
		tripaction.ErrNoOption:   connect.CodeInvalidArgument,
		trip.ErrInvalidEdit:      connect.CodeInvalidArgument,
	} {
		h := (&ChatHandler{}).WithTripActions(fakeTripActions{err: err})
		_, got := h.ApplyTripAction(signedIn(), connect.NewRequest(&chatv1.ApplyTripActionRequest{ProposalId: uuid.NewString()}))
		require.Equal(t, code, connect.CodeOf(got), err.Error())
	}
	_, err := (&ChatHandler{}).ApplyTripAction(signedIn(), connect.NewRequest(&chatv1.ApplyTripActionRequest{ProposalId: uuid.NewString()}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	_, err = (&ChatHandler{}).WithTripActions(fakeTripActions{}).DismissTripAction(context.Background(),
		connect.NewRequest(&chatv1.DismissTripActionRequest{ProposalId: uuid.NewString()}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
