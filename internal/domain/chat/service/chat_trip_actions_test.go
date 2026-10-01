package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type fakeProposer struct {
	out []tripaction.Proposal
	err error
	got string
}

func (f *fakeProposer) Propose(_ context.Context, _, _, _ uuid.UUID, msg string) ([]tripaction.Proposal, error) {
	f.got = msg
	return f.out, f.err
}

func drainEvents(ch chan locitypes.StreamEvent) []locitypes.StreamEvent {
	close(ch)
	var evs []locitypes.StreamEvent
	for e := range ch {
		evs = append(evs, e)
	}
	return evs
}

func boundTurn(ch chan locitypes.StreamEvent, msg string) common.ChatContext {
	return common.ChatContext{Ctx: context.Background(), UserID: uuid.New(), TripID: uuid.New(), Message: msg, EventCh: ch, CityName: "Lisbon"}
}

func TestBoundTurn_ProposalsReplaceGeneration(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) {
		t.Fatal("a turn that proposes changes must not regenerate")
		return nil, nil
	}
	l.SetTripActions(&fakeProposer{out: []tripaction.Proposal{{ID: uuid.New(), Summary: "Set dates"}, {ID: uuid.New(), Summary: "Hotels"}}})
	ch := make(chan locitypes.StreamEvent, 10)
	require.NoError(t, l.ProcessUnifiedChatMessageStream(boundTurn(ch, "12 to 17 Nov, 4-star hotels")))
	evs := drainEvents(ch)
	require.Len(t, evs, 3)
	require.Equal(t, locitypes.EventTypeActionProposal, evs[0].Type)
	require.Equal(t, "Set dates", evs[0].Message)
	require.IsType(t, tripaction.Proposal{}, evs[0].Data)
	require.Equal(t, locitypes.EventTypeComplete, evs[2].Type)
	require.True(t, evs[2].IsFinal)
}

func TestBoundTurn_NoChangeAnswersButNeverSavesANewTrip(t *testing.T) {
	for name, p := range map[string]*fakeProposer{
		"nothing asked":     {},
		"extraction failed": {err: errors.New("model down")},
	} {
		t.Run(name, func(t *testing.T) {
			l := newStreamService(t, &TestLLMClient{})
			l.SetTripActions(p)
			var ran common.ChatContext
			l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
				ran = cc
				return &locitypes.AiCityResponse{}, nil
			}
			ch := make(chan locitypes.StreamEvent, 10)
			cc := boundTurn(ch, "what's good for dinner?")
			cc.StopRun = true // skip multi-city planning in this unit test
			require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
			drainEvents(ch)
			require.True(t, ran.SuppressTripSave, "a trip-bound turn never creates a trip")
			require.Equal(t, cc.TripID, ran.TripID)
		})
	}
}

func TestUnboundTurn_NeverAsksTheProposer(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	p := &fakeProposer{}
	l.SetTripActions(p)
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		require.False(t, cc.SuppressTripSave)
		return &locitypes.AiCityResponse{}, nil
	}
	ch := make(chan locitypes.StreamEvent, 10)
	cc := boundTurn(ch, "3 days in Lisbon")
	cc.TripID, cc.StopRun = uuid.Nil, true
	require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
	drainEvents(ch)
	require.Empty(t, p.got)
}

func TestGenerateDays_RunsAPresetCityWithoutSaving(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		require.True(t, cc.StopRun)
		require.True(t, cc.SuppressTripSave)
		require.Equal(t, 2, cc.PresetTripDays)
		require.Equal(t, "Lisbon", cc.CityName)
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeProgress} // nobody reads a re-plan's stream; must not block
		return &locitypes.AiCityResponse{PointsOfInterest: []locitypes.POIDetailedInfo{
			{Name: "Belém", Day: 1}, {Name: "Alfama", Day: 1}, {Name: "Sintra", Day: 2},
		}}, nil
	}
	days, err := l.GenerateDays(context.Background(), uuid.New(), "Lisbon", 2)
	require.NoError(t, err)
	require.Len(t, days, 2)
	require.Len(t, days[0].Stops, 2)
}

func TestGenerateDays_NothingGenerated(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) { return &locitypes.AiCityResponse{}, nil }
	_, err := l.GenerateDays(context.Background(), uuid.New(), "Lisbon", 2)
	require.Error(t, err)
}
