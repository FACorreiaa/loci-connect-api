package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// scriptedModel plays back turns and records what it was sent. Tests never
// reach a real API.
type scriptedModel struct {
	mu    sync.Mutex
	turns []scriptedTurn
	reqs  []anthropic.ToolTurnRequest
}

type scriptedTurn struct {
	text string
	stop string
	uses []anthropic.ToolBlock
	err  error
}

func (m *scriptedModel) ToolTurn(_ context.Context, req anthropic.ToolTurnRequest, onText func(string)) (*anthropic.ToolTurnResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs = append(m.reqs, req)
	if len(m.turns) == 0 {
		return nil, errors.New("script ran out")
	}
	turn := m.turns[0]
	m.turns = m.turns[1:]
	if turn.text != "" {
		onText(turn.text)
	}
	if turn.err != nil {
		return nil, turn.err
	}
	blocks := append([]anthropic.ToolBlock{}, turn.uses...)
	if turn.text != "" {
		blocks = append([]anthropic.ToolBlock{{Type: anthropic.BlockText, Text: turn.text}}, blocks...)
	}
	return &anthropic.ToolTurnResult{
		Message:    anthropic.ToolMessage{Role: anthropic.RoleAssistant, Blocks: blocks},
		StopReason: turn.stop,
		Model:      "claude-haiku-5-5",
		Usage:      anthropic.ToolUsage{InputTokens: 1000, OutputTokens: 100},
	}, nil
}

func use(id, name, input string) anthropic.ToolBlock {
	return anthropic.ToolBlock{Type: anthropic.BlockToolUse, ToolUseID: id, ToolName: name, Input: json.RawMessage(input)}
}

type memStore struct {
	mu    sync.Mutex
	asks  map[uuid.UUID]int
	spend Spend
	day   int64
	convs map[uuid.UUID]memConv
}

type memConv struct {
	user    uuid.UUID
	turns   []Turn
	expires time.Time
}

func newMemStore() *memStore {
	return &memStore{asks: map[uuid.UUID]int{}, convs: map[uuid.UUID]memConv{}}
}

func (m *memStore) ReserveAsk(_ context.Context, u uuid.UUID, _ time.Time, limit int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.asks[u] >= limit {
		return false, nil
	}
	m.asks[u]++
	return true, nil
}

func (m *memStore) AddSpend(_ context.Context, _ uuid.UUID, _ time.Time, s Spend) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spend.InputTokens += s.InputTokens
	m.spend.OutputTokens += s.OutputTokens
	m.spend.CostMicroUSD += s.CostMicroUSD
	return nil
}

func (m *memStore) DaySpendMicroUSD(context.Context, time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.day + m.spend.CostMicroUSD, nil
}

func (m *memStore) LoadTurns(_ context.Context, u, id uuid.UUID, now time.Time) ([]Turn, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.convs[id]
	if !ok || c.user != u || !c.expires.After(now) {
		return nil, false, nil
	}
	return c.turns, true, nil
}

func (m *memStore) SaveTurns(_ context.Context, u uuid.UUID, id *uuid.UUID, turns []Turn, expires time.Time) (uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != nil {
		if c, ok := m.convs[*id]; ok && c.user == u {
			m.convs[*id] = memConv{u, turns, expires}
			return *id, nil
		}
	}
	created := uuid.New()
	m.convs[created] = memConv{u, turns, expires}
	return created, nil
}

// fakeTrips answers for one owner, and reads as not found for everybody else.
type fakeTrips struct {
	owner uuid.UUID
	t     *trip.Trip
}

func (f *fakeTrips) ListTrips(_ context.Context, u uuid.UUID, _, _ int) ([]*trip.Trip, int, error) {
	if u != f.owner {
		return nil, 0, nil
	}
	return []*trip.Trip{f.t}, 1, nil
}

func (f *fakeTrips) Resolve(_ context.Context, viewer, id uuid.UUID) (*trip.Trip, trip.Role, error) {
	if viewer != f.owner || id != f.t.ID {
		return nil, trip.RoleNone, trip.ErrNotFound
	}
	return f.t, trip.RoleOwner, nil
}

// fakeFallback is the paid primary: it records the prompt and answers.
type fakeFallback struct {
	generativeAI.ChatClient
	prompt string
	system string
	text   string
	err    error
}

func (f *fakeFallback) GenerateStream(_ context.Context, prompt string, cfg *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	f.prompt, f.system = prompt, cfg.SystemInstruction.Parts[0].Text
	if f.err != nil {
		return nil, f.err
	}
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		yield(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: f.text}}},
		}}}, nil)
	}, nil
}

type fixture struct {
	svc   *Service
	model *scriptedModel
	store *memStore
	fall  *fakeFallback
	trips *fakeTrips
	user  uuid.UUID
}

func newFixture(t *testing.T, turns ...scriptedTurn) *fixture {
	t.Helper()
	user := uuid.New()
	trips := &fakeTrips{owner: user, t: &trip.Trip{ID: uuid.New(), Title: "Lisbon in May", CityName: "Lisbon"}}
	f := &fixture{
		model: &scriptedModel{turns: turns},
		store: newMemStore(),
		fall:  &fakeFallback{text: "From what I have, Lisbon."},
		trips: trips,
		user:  user,
	}
	f.svc = NewService(Deps{
		Model: f.model, Fallback: f.fall, Store: f.store,
		Sources: Sources{Trips: trips, Members: trips},
		Now:     func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) },
	})
	return f
}

type result struct {
	text     string
	statuses []string
	done     *assistantv1.Done
}

func (f *fixture) ask(t *testing.T, req *assistantv1.AskAssistantRequest) result {
	t.Helper()
	r, err := f.askAs(f.user, req)
	require.NoError(t, err)
	return r
}

func (f *fixture) askAs(user uuid.UUID, req *assistantv1.AskAssistantRequest) (result, error) {
	var r result
	err := f.svc.Ask(context.Background(), user, req, func(e *assistantv1.AssistantEvent) error {
		switch ev := e.GetEvent().(type) {
		case *assistantv1.AssistantEvent_TextDelta:
			r.text += ev.TextDelta.GetText()
		case *assistantv1.AssistantEvent_ToolStatus:
			r.statuses = append(r.statuses, ev.ToolStatus.GetLabel())
		case *assistantv1.AssistantEvent_Done:
			r.done = ev.Done
		}
		return nil
	})
	return r, err
}

func question(text string) *assistantv1.AskAssistantRequest {
	return &assistantv1.AskAssistantRequest{Text: text}
}

func lastUserText(req anthropic.ToolTurnRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == anthropic.RoleUser && req.Messages[i].Blocks[0].Type == anthropic.BlockText {
			return req.Messages[i].Blocks[0].Text
		}
	}
	return ""
}

func TestAsk_AnswersWithoutTools(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "Bring a jacket.", stop: "end_turn"})
	r := f.ask(t, question("what's the weather like in November?"))
	require.Equal(t, "Bring a jacket.", r.text)
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_COMPLETE, r.done.GetReason())
	require.False(t, r.done.GetDegraded())
	require.NotEmpty(t, r.done.GetConversationId())
	require.Equal(t, int64(150), f.store.spend.CostMicroUSD, "1000 in at $0.10, 100 out at $0.50")
}

func TestAsk_RunsToolsAndFeedsResultsBack(t *testing.T) {
	f := newFixture(t,
		scriptedTurn{text: "Let me look.", stop: "tool_use", uses: []anthropic.ToolBlock{use("t1", "list_trips", `{"limit":5}`)}},
		scriptedTurn{text: "You're going to Lisbon.", stop: "end_turn"},
	)
	r := f.ask(t, question("where am I going?"))
	require.Equal(t, "Let me look. You're going to Lisbon.", r.text, "two model calls read as one answer")
	require.Equal(t, []string{"Checking your trips"}, r.statuses)

	second := f.model.reqs[1]
	results := second.Messages[len(second.Messages)-1]
	require.Equal(t, anthropic.RoleUser, results.Role)
	require.Len(t, results.Blocks, 1)
	require.Equal(t, "t1", results.Blocks[0].ToolUseID)
	require.False(t, results.Blocks[0].IsError)
	require.Contains(t, results.Blocks[0].Result, "Lisbon in May")
}

// The registry is what the model can reach, so "it cannot write" is a property
// of this list. Proposal tools belong to a later slice and write nothing either.
func TestReadTools_OfferNothingThatWrites(t *testing.T) {
	src := Sources{
		Trips: &fakeTrips{}, Members: &fakeTrips{}, Lists: nilLists{}, POIs: nilPOIs{},
		Memory: nilMemory{}, Friends: nilFriends{}, Visits: nilVisits{}, Activity: nilActivity{},
	}
	var names []string
	for _, tool := range readTools(src) {
		names = append(names, tool.def.Name)
		require.NotEmpty(t, tool.status, tool.def.Name)
		for key := range tool.def.Properties {
			require.NotContains(t, strings.ToLower(key), "user", "the caller comes from the request, never from the model")
		}
	}
	require.ElementsMatch(t, []string{
		"list_trips", "get_trip", "list_user_lists", "get_list", "list_favorites", "search_pois",
		"get_poi_details", "get_memory", "list_friends", "get_travel_history", "list_recents",
	}, names)
}

func TestTools_ACallerReachesOnlyTheirOwn(t *testing.T) {
	f := newFixture(t,
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("t1", "get_trip", `{"id":"PLACEHOLDER"}`)}},
		scriptedTurn{text: "I can't find that trip.", stop: "end_turn"},
	)
	f.model.turns[0].uses[0].Input = json.RawMessage(`{"id":"` + f.trips.t.ID.String() + `"}`)

	r, err := f.askAs(uuid.New(), question("show me that trip"))
	require.NoError(t, err)
	require.Equal(t, "I can't find that trip.", r.text)
	result := f.model.reqs[1].Messages[2].Blocks[0]
	require.True(t, result.IsError)
	require.Equal(t, "no trip with that id", result.Result)
	require.NotContains(t, result.Result, "Lisbon")
}

func TestContext_EntitiesAreCheckedBeforeBeingNamed(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "ok", stop: "end_turn"}, scriptedTurn{text: "ok", stop: "end_turn"})
	req := func() *assistantv1.AskAssistantRequest {
		return &assistantv1.AskAssistantRequest{Text: "and this?", Context: &assistantv1.ClientContext{
			Screen: "trip_detail",
			Entities: []*assistantv1.ContextEntity{{
				Type: assistantv1.ContextEntityType_CONTEXT_ENTITY_TYPE_TRIP, Id: f.trips.t.ID.String(),
			}},
		}}
	}

	f.ask(t, req())
	require.Contains(t, lastUserText(f.model.reqs[0]), "Lisbon in May")

	_, err := f.askAs(uuid.New(), req())
	require.NoError(t, err)
	require.NotContains(t, lastUserText(f.model.reqs[1]), "Lisbon in May", "a client naming a trip does not get to read it")
}

func TestContext_NoLocationIsSaidAndNeverDefaulted(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "Where are you?", stop: "end_turn"})
	f.ask(t, &assistantv1.AskAssistantRequest{Text: "what's near me?", Context: &assistantv1.ClientContext{Screen: "discover"}})
	prompt := lastUserText(f.model.reqs[0])
	require.Contains(t, prompt, "Location: unknown")
	require.NotContains(t, prompt, "38.7", "Lisbon is not a default")
}

func TestContext_StaysOutOfTheCachedPrefix(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "ok", stop: "end_turn"}, scriptedTurn{text: "ok", stop: "end_turn"})
	f.ask(t, &assistantv1.AskAssistantRequest{Text: "a", Context: &assistantv1.ClientContext{Screen: "discover"}})
	f.ask(t, &assistantv1.AskAssistantRequest{Text: "b", Context: &assistantv1.ClientContext{Screen: "saved"}})
	require.Equal(t, f.model.reqs[0].System, f.model.reqs[1].System)
	require.NotContains(t, f.model.reqs[0].System, "discover")
}

func TestLoop_StopsCallingToolsAtTheLimit(t *testing.T) {
	loop := scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("t", "list_trips", `{"limit":1}`)}}
	f := newFixture(t, loop, loop, loop, scriptedTurn{text: "Enough.", stop: "end_turn"})
	r := f.ask(t, question("x"))
	require.Equal(t, "Enough.", r.text)
	require.Len(t, f.model.reqs, DefaultLimits().ModelCalls)
	require.True(t, f.model.reqs[len(f.model.reqs)-1].ForbidTools, "the last call has to answer")
	require.False(t, f.model.reqs[0].ForbidTools)
}

func TestLoop_ToolCallsPerAskAreCapped(t *testing.T) {
	var uses []anthropic.ToolBlock
	for range DefaultLimits().ToolCalls + 2 {
		uses = append(uses, use(uuid.NewString(), "list_trips", `{"limit":1}`))
	}
	f := newFixture(t, scriptedTurn{stop: "tool_use", uses: uses}, scriptedTurn{text: "ok", stop: "end_turn"})
	f.ask(t, question("x"))
	results := f.model.reqs[1].Messages[2].Blocks
	require.Len(t, results, len(uses), "the API needs a result for every call")
	require.True(t, results[len(results)-1].IsError)
	require.Contains(t, results[len(results)-1].Result, "too many")
	require.False(t, results[0].IsError)
}

func TestLoop_UnknownToolIsAResultNotACrash(t *testing.T) {
	f := newFixture(t,
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("t1", "delete_everything", `{}`)}},
		scriptedTurn{text: "I can't do that.", stop: "end_turn"},
	)
	r := f.ask(t, question("x"))
	require.Equal(t, "I can't do that.", r.text)
	require.True(t, f.model.reqs[1].Messages[2].Blocks[0].IsError)
}

func TestLoop_LongToolOutputIsCut(t *testing.T) {
	f := newFixture(t,
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("t1", "list_trips", `{"limit":1}`)}},
		scriptedTurn{text: "ok", stop: "end_turn"},
	)
	f.trips.t.Title = strings.Repeat("é", 10000)
	f.ask(t, question("x"))
	result := f.model.reqs[1].Messages[2].Blocks[0].Result
	require.LessOrEqual(t, len(result), DefaultLimits().ToolOutput+32)
	require.Contains(t, result, "cut short")
	require.True(t, strings.ToValidUTF8(result, "") == result, "cut on a rune boundary")
}

func TestStop_RefusalEndsWithoutFailingOver(t *testing.T) {
	f := newFixture(t, scriptedTurn{stop: "refusal"})
	r := f.ask(t, question("x"))
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_REFUSED, r.done.GetReason())
	require.Empty(t, f.fall.prompt, "a weaker model is no answer to a decline")
}

func TestStop_MaxTokensIsInterrupted(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "Lis", stop: "max_tokens", uses: []anthropic.ToolBlock{use("t1", "list_trips", `{"lim`)}})
	r := f.ask(t, question("x"))
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_INTERRUPTED, r.done.GetReason())
	require.Len(t, f.model.reqs, 1, "a tool input cut off by max_tokens must not run")
}

func TestFailure_BeforeTextGoesDegraded(t *testing.T) {
	f := newFixture(t, scriptedTurn{err: llmerrors.ErrUnavailable})
	r := f.ask(t, question("where am I going?"))
	require.Equal(t, "From what I have, Lisbon.", r.text)
	require.True(t, r.done.GetDegraded())
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_COMPLETE, r.done.GetReason())
	require.Contains(t, f.fall.prompt, "Lisbon in May", "the snapshot is the only way it knows")
	require.Contains(t, f.fall.system, "tools are unavailable")
}

func TestFailure_AfterTextKeepsWhatWasSaid(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "You're going to", err: llmerrors.ErrUnavailable})
	r := f.ask(t, question("x"))
	require.Equal(t, "You're going to", r.text)
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_INTERRUPTED, r.done.GetReason())
	require.False(t, r.done.GetDegraded())
	require.Empty(t, f.fall.prompt, "changing voices part way would be worse than stopping")
}

func TestFailure_NothingAtAllIsUnavailable(t *testing.T) {
	f := newFixture(t, scriptedTurn{err: llmerrors.ErrUnavailable})
	f.fall.err = errors.New("also down")
	r := f.ask(t, question("x"))
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_UNAVAILABLE, r.done.GetReason())
}

func TestFailure_SpentBudgetCoolsDown(t *testing.T) {
	f := newFixture(t, scriptedTurn{err: llmerrors.ErrOutOfCredits})
	f.ask(t, question("x"))
	require.Len(t, f.model.reqs, 1)
	r := f.ask(t, question("y"))
	require.Len(t, f.model.reqs, 1, "no paying for a failed call on every ask")
	require.True(t, r.done.GetDegraded())
}

func TestDegraded_NoModelConfiguredGoesStraightThere(t *testing.T) {
	f := newFixture(t)
	f.svc.d.Model = nil
	r := f.ask(t, question("x"))
	require.True(t, r.done.GetDegraded())
}

func TestSpend_DailyCeilingProtectsTheSharedBudget(t *testing.T) {
	f := newFixture(t)
	f.store.day = defaultDailyCeilingMicroUSD
	r := f.ask(t, question("x"))
	require.Empty(t, f.model.reqs)
	require.True(t, r.done.GetDegraded())
}

func TestSpend_PersonalDailyLimit(t *testing.T) {
	f := newFixture(t)
	f.store.asks[f.user] = defaultDailyAsks
	r := f.ask(t, question("x"))
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_DAILY_LIMIT, r.done.GetReason())
	require.Empty(t, f.model.reqs)
	require.Empty(t, f.fall.prompt)
}

func TestConversation_CarriesOnAndKeepsOnlyText(t *testing.T) {
	f := newFixture(t,
		scriptedTurn{text: "Checking.", stop: "tool_use", uses: []anthropic.ToolBlock{use("t1", "list_trips", `{"limit":1}`)}},
		scriptedTurn{text: "Lisbon.", stop: "end_turn"},
		scriptedTurn{text: "In May.", stop: "end_turn"},
	)
	first := f.ask(t, question("where?"))
	id := first.done.GetConversationId()

	stored := f.store.convs[uuid.MustParse(id)]
	require.Equal(t, []Turn{
		{Role: anthropic.RoleUser, Text: "where?"},
		{Role: anthropic.RoleAssistant, Text: "Checking. Lisbon."},
	}, stored.turns, "no tool output and no context is kept")
	require.Equal(t, f.svc.d.Now().Add(conversationTTL), stored.expires)

	second := f.ask(t, &assistantv1.AskAssistantRequest{ConversationId: &id, Text: "when?"})
	require.Equal(t, id, second.done.GetConversationId())
	sent := f.model.reqs[2].Messages
	require.Equal(t, "where?", sent[0].Blocks[0].Text)
	require.Equal(t, "Checking. Lisbon.", sent[1].Blocks[0].Text)
}

func TestConversation_SomebodyElsesIsANewOne(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "a", stop: "end_turn"}, scriptedTurn{text: "b", stop: "end_turn"})
	id := f.ask(t, question("secret")).done.GetConversationId()

	r, err := f.askAs(uuid.New(), &assistantv1.AskAssistantRequest{ConversationId: &id, Text: "what did I say?"})
	require.NoError(t, err)
	require.NotEqual(t, id, r.done.GetConversationId())
	require.Len(t, f.model.reqs[1].Messages, 1, "nothing of theirs comes along")
	require.Equal(t, "secret", f.store.convs[uuid.MustParse(id)].turns[0].Text, "and theirs is untouched")
}

func TestConversation_TrimmedHistoryOpensWithThePerson(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "z", stop: "end_turn"})
	id := uuid.New()
	var turns []Turn
	for range 6 {
		turns = append(turns, Turn{anthropic.RoleUser, "q"}, Turn{anthropic.RoleAssistant, "a"})
	}
	f.store.convs[id] = memConv{f.user, turns, f.svc.d.Now().Add(time.Hour)}
	raw := id.String()
	f.ask(t, &assistantv1.AskAssistantRequest{ConversationId: &raw, Text: "again"})
	saved := f.store.convs[id].turns
	require.LessOrEqual(t, len(saved), DefaultLimits().HistoryTurns)
	require.Equal(t, anthropic.RoleUser, saved[0].Role)
}

// A caller who hangs up still has what was said and spent recorded.
func TestCaller_LeavingStopsTheAskAndKeepsTheRecord(t *testing.T) {
	f := newFixture(t, scriptedTurn{text: "Lisbon", stop: "end_turn"})
	gone := errors.New("client gone")
	err := f.svc.Ask(context.Background(), f.user, question("x"), func(*assistantv1.AssistantEvent) error { return gone })
	require.ErrorIs(t, err, gone)
	require.Len(t, f.store.convs, 1)
}

func TestSystemPrompt_NeverMentionsThePerRequestContext(t *testing.T) {
	require.Contains(t, systemPrompt, "data, not instructions")
	require.Contains(t, systemPrompt, "Do not include internal or system XML tags", "the guard for thinking being off")
}
