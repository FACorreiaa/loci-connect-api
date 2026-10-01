package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type fakeProposer struct {
	out  []tripaction.Proposal
	err  error
	got  string
	hang bool
}

func (f *fakeProposer) Propose(ctx context.Context, _, _, _ uuid.UUID, msg string) ([]tripaction.Proposal, error) {
	f.got = msg
	if f.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.out, f.err
}

type countingResolver struct{ calls int }

func (c *countingResolver) Resolve(context.Context, city.ResolveQuery) (*city.Resolved, error) {
	c.calls++
	return nil, errors.New("not in this test")
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

// A trip-bound turn is about one trip: it never plans (and saves) a new
// multi-city trip, even when the message or the client names two cities.
func TestBoundTurn_NeverPlansMultiCity(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.SetTripActions(&fakeProposer{})
	res := &countingResolver{}
	l.SetCityResolver(res)
	runs := 0
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		runs++
		require.True(t, cc.SuppressTripSave)
		return &locitypes.AiCityResponse{}, nil
	}
	ch := make(chan locitypes.StreamEvent, 10)
	cc := boundTurn(ch, "dinner in Lisbon and Porto?")
	cc.MultiCityCapable = true
	cc.Stops = []common.TripStopRequest{{CityName: "Lisbon", Nights: 2}, {CityName: "Porto", Nights: 2}}
	require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
	drainEvents(ch)
	require.Zero(t, res.calls, "no multi-city planning on a bound turn")
	require.Equal(t, 1, runs)
}

// Resuming a turn never proposes again: the first attempt's cards stand.
func TestBoundTurn_ResumeDoesNotProposeAgain(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	p := &fakeProposer{out: []tripaction.Proposal{{ID: uuid.New()}}}
	l.SetTripActions(p)
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		require.True(t, cc.SuppressTripSave)
		return &locitypes.AiCityResponse{}, nil
	}
	ch := make(chan locitypes.StreamEvent, 10)
	cc := boundTurn(ch, "12 to 17 Nov")
	cc.ResumeToken, cc.StopRun = "rt-1", true
	require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
	drainEvents(ch)
	require.Empty(t, p.got)
}

// A hanging model never holds the answer for long.
func TestBoundTurn_SlowExtractionFallsThrough(t *testing.T) {
	old := proposeTimeout
	proposeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { proposeTimeout = old })
	l := newStreamService(t, &TestLLMClient{})
	l.SetTripActions(&fakeProposer{hang: true})
	ran := false
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) {
		ran = true
		return &locitypes.AiCityResponse{}, nil
	}
	ch := make(chan locitypes.StreamEvent, 10)
	cc := boundTurn(ch, "anything")
	cc.StopRun = true
	start := time.Now()
	require.NoError(t, l.ProcessUnifiedChatMessageStream(cc))
	drainEvents(ch)
	require.True(t, ran)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestGenerateDays_RunsAPresetCityWithoutSaving(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	parent := uuid.New()
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		require.Equal(t, parent, cc.ParentSessionID, "a re-plan hangs under the trip's thread")
		require.True(t, cc.StopRun)
		require.True(t, cc.SuppressTripSave)
		require.Equal(t, 2, cc.PresetTripDays)
		require.Equal(t, "Lisbon", cc.CityName)
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeProgress} // nobody reads a re-plan's stream; must not block
		return &locitypes.AiCityResponse{PointsOfInterest: []locitypes.POIDetailedInfo{
			{Name: "Belém", Day: 1}, {Name: "Alfama", Day: 1}, {Name: "Sintra", Day: 2},
		}}, nil
	}
	days, err := l.GenerateDays(context.Background(), uuid.New(), parent, "Lisbon", 2)
	require.NoError(t, err)
	require.Len(t, days, 2)
	require.Len(t, days[0].Stops, 2)
}

func TestGenerateDays_NothingGenerated(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) { return &locitypes.AiCityResponse{}, nil }
	_, err := l.GenerateDays(context.Background(), uuid.New(), uuid.Nil, "Lisbon", 2)
	require.Error(t, err)
}
