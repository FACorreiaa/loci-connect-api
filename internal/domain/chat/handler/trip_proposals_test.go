package handler

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
)

type fakeProposals struct {
	got tripaction.Action
	err error
}

func (f *fakeProposals) ProposeEdit(_ context.Context, _, tripID uuid.UUID, a tripaction.Action, base int64) (*tripaction.View, error) {
	f.got = a
	if f.err != nil {
		return nil, f.err
	}
	return &tripaction.View{Proposal: tripaction.Proposal{ID: uuid.New(), TripID: tripID, Action: a, BaseVersion: base, Source: tripaction.SourceEditor}}, nil
}

func (f *fakeProposals) List(context.Context, uuid.UUID, uuid.UUID, bool) ([]tripaction.View, error) {
	return []tripaction.View{{Tally: tripaction.Tally{Up: 2}}}, f.err
}

func (f *fakeProposals) React(_ context.Context, _, id uuid.UUID, v int) (*tripaction.View, error) {
	return &tripaction.View{Proposal: tripaction.Proposal{ID: id}, Tally: tripaction.Tally{Mine: v}}, f.err
}

func TestProposeTripChange(t *testing.T) {
	f := &fakeProposals{}
	h := (&ChatHandler{}).WithTripProposals(f)
	stop := uuid.NewString()
	res, err := h.ProposeTripChange(signedIn(), connect.NewRequest(&chatv1.ProposeTripChangeRequest{
		TripId: uuid.NewString(), BaseVersion: 5,
		Action: &chatv1.TripAction{Kind: &chatv1.TripAction_RemoveStop{RemoveStop: &chatv1.RemoveStopAction{StopId: stop}}},
	}))
	require.NoError(t, err)
	require.Equal(t, tripaction.KindRemoveStop, f.got.Kind)
	require.Equal(t, stop, f.got.StopID)
	require.Equal(t, chatv1.ProposalSource_PROPOSAL_SOURCE_EDITOR, res.Msg.GetSource())
	require.Equal(t, stop, res.Msg.GetAction().GetRemoveStop().GetStopId())

	_, err = h.ProposeTripChange(signedIn(), connect.NewRequest(&chatv1.ProposeTripChangeRequest{
		TripId: uuid.NewString(),
		Action: &chatv1.TripAction{Kind: &chatv1.TripAction_RegenerateDays{RegenerateDays: &chatv1.RegenerateDaysAction{Days: 3}}},
	}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "only stop edits come from the trip")

	f.err = tripaction.ErrForbidden
	_, err = h.ProposeTripChange(signedIn(), connect.NewRequest(&chatv1.ProposeTripChangeRequest{
		TripId: uuid.NewString(),
		Action: &chatv1.TripAction{Kind: &chatv1.TripAction_RemoveStop{RemoveStop: &chatv1.RemoveStopAction{StopId: stop}}},
	}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestListAndReactToTripProposals(t *testing.T) {
	h := (&ChatHandler{}).WithTripProposals(&fakeProposals{})
	list, err := h.ListTripProposals(signedIn(), connect.NewRequest(&chatv1.ListTripProposalsRequest{TripId: uuid.NewString()}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetProposals(), 1)
	require.EqualValues(t, 2, list.Msg.GetProposals()[0].GetUpVotes())

	r, err := h.ReactToTripProposal(signedIn(), connect.NewRequest(&chatv1.ReactToTripProposalRequest{ProposalId: uuid.NewString(), Value: -1}))
	require.NoError(t, err)
	require.EqualValues(t, -1, r.Msg.GetMyReaction())

	_, err = h.ListTripProposals(signedIn(), connect.NewRequest(&chatv1.ListTripProposalsRequest{TripId: "nope"}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = h.ListTripProposals(context.Background(), connect.NewRequest(&chatv1.ListTripProposalsRequest{TripId: uuid.NewString()}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = (&ChatHandler{}).ListTripProposals(signedIn(), connect.NewRequest(&chatv1.ListTripProposalsRequest{TripId: uuid.NewString()}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}
