package assistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	assistantv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/assistant"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/pkg/anthropic"
)

// searchingModel answers the assistant's own calls from a script and the
// search_web call, the only one sent with WebSearch, from search.
type searchingModel struct {
	*scriptedModel
	search    *anthropic.ToolTurnResult
	searchErr error
	searches  []anthropic.ToolTurnRequest
}

func (m *searchingModel) ToolTurn(ctx context.Context, req anthropic.ToolTurnRequest, onText func(string)) (*anthropic.ToolTurnResult, error) {
	if req.WebSearch == nil {
		return m.scriptedModel.ToolTurn(ctx, req, onText)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searches = append(m.searches, req)
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.search, nil
}

func searchResult(text string) *anthropic.ToolTurnResult {
	return &anthropic.ToolTurnResult{
		Message: anthropic.ToolMessage{Role: anthropic.RoleAssistant, Blocks: []anthropic.ToolBlock{
			{Type: anthropic.BlockText, Text: text},
		}},
		StopReason: "end_turn",
		Model:      "claude-haiku-5-5",
		Usage:      anthropic.ToolUsage{InputTokens: 2000, OutputTokens: 200, WebSearchRequests: 2},
		WebSources: []anthropic.WebSource{
			{URL: "https://www.visitlisboa.com/en/places/museu-nacional-do-azulejo", Title: "Museu Nacional do Azulejo", PageAge: "3 days ago"},
			{URL: "https://www.museudoazulejo.gov.pt/horario", Title: "Horário"},
		},
	}
}

func newSearchFixture(t *testing.T, cfg WebSearch, search *anthropic.ToolTurnResult, turns ...scriptedTurn) (*fixture, *searchingModel) {
	t.Helper()
	user := uuid.New()
	trips := &fakeTrips{owner: user, t: &trip.Trip{ID: uuid.New(), Title: "Lisbon in May", CityName: "Lisbon"}}
	model := &searchingModel{scriptedModel: &scriptedModel{turns: turns}, search: search}
	f := &fixture{model: model.scriptedModel, store: newMemStore(), fall: &fakeFallback{text: "From what I have."}, trips: trips, user: user}
	f.svc = NewService(Deps{
		Model: model, Fallback: f.fall, Store: f.store,
		Sources:   Sources{Trips: trips, Members: trips},
		Now:       func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
		WebSearch: cfg,
	})
	return f, model
}

func TestSearchWeb_OffUnlessEnabled(t *testing.T) {
	f := newFixture(t)
	_, offered := f.svc.toolByName[searchWebTool]
	require.False(t, offered, "a search costs money: it is never on by default")

	noModel := NewService(Deps{Store: newMemStore(), WebSearch: WebSearch{Enabled: true}})
	_, offered = noModel.toolByName[searchWebTool]
	require.False(t, offered, "nothing to search with")
}

func TestSearchWeb_DigestComesBackAsATextResult(t *testing.T) {
	f, model := newSearchFixture(t, WebSearch{Enabled: true},
		searchResult("Open Tuesday to Sunday, 10:00 to 18:00; closed Mondays."),
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("s1", searchWebTool, `{"query":"Museu Nacional do Azulejo opening hours"}`)}},
		scriptedTurn{text: "It's open until six, closed Mondays, says Visit Lisboa.", stop: "end_turn"},
	)
	r := f.ask(t, question("is the tile museum open on Monday?"))
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_COMPLETE, r.done.GetReason())
	require.Equal(t, []string{"Searching the web"}, r.statuses)

	require.Len(t, model.searches, 1)
	sub := model.searches[0]
	require.Equal(t, searchSystem, sub.System)
	require.Empty(t, sub.Tools, "the search call reaches none of their data")
	require.Equal(t, int64(2), sub.WebSearch.MaxUses)
	require.Len(t, sub.Messages, 1)
	require.Contains(t, sub.Messages[0].Blocks[0].Text, "Museu Nacional do Azulejo opening hours")
	require.Contains(t, sub.Messages[0].Blocks[0].Text, "Thursday 19 November 2026")
	require.NotContains(t, sub.Messages[0].Blocks[0].Text, "<context>", "where they are and who they are stay in the assistant")

	second := f.model.reqs[1]
	got := second.Messages[len(second.Messages)-1].Blocks[0]
	require.Equal(t, "s1", got.ToolUseID)
	require.False(t, got.IsError)
	require.Contains(t, got.Result, "closed Mondays")
	require.Contains(t, got.Result, `"site":"visitlisboa.com"`)
	require.Contains(t, got.Result, "3 days ago")
	require.NotContains(t, got.Result, "https://", "no links: the answer is spoken")

	// Two assistant calls at 1000 in and 100 out, the search call at 2000 and
	// 200, and two searches at a cent each.
	require.Equal(t, int64(2*150+300+2*10_000), f.store.spend.CostMicroUSD)
}

func TestSearchWeb_DailyCapsAreAResultTheModelCanSay(t *testing.T) {
	f, model := newSearchFixture(t, WebSearch{Enabled: true, PerUserDaily: 1},
		searchResult("Open until six."),
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("s1", searchWebTool, `{"query":"tile museum hours Lisbon"}`)}},
		scriptedTurn{text: "Until six.", stop: "end_turn"},
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("s2", searchWebTool, `{"query":"Oceanário hours Lisbon"}`)}},
		scriptedTurn{text: "I can't check that today.", stop: "end_turn"},
	)
	f.ask(t, question("tile museum hours?"))
	f.ask(t, question("and the aquarium?"))

	require.Len(t, model.searches, 1, "the second search never reached Anthropic")
	last := f.model.reqs[len(f.model.reqs)-1]
	got := last.Messages[len(last.Messages)-1].Blocks[0]
	require.True(t, got.IsError)
	require.Contains(t, got.Result, "no more web searches today")
}

func TestSearchWeb_AFailedSearchIsAnErrorResult(t *testing.T) {
	f, model := newSearchFixture(t, WebSearch{Enabled: true}, nil,
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("s1", searchWebTool, `{"query":"Lisbon metro strike"}`)}},
		scriptedTurn{text: "I couldn't check just now.", stop: "end_turn"},
	)
	model.searchErr = errors.New("anthropic: 529 overloaded")
	r := f.ask(t, question("is there a metro strike?"))
	require.Equal(t, assistantv1.DoneReason_DONE_REASON_COMPLETE, r.done.GetReason())

	second := f.model.reqs[1]
	got := second.Messages[len(second.Messages)-1].Blocks[0]
	require.True(t, got.IsError)
	require.Equal(t, "that lookup failed", got.Result, "the provider's message is not the model's business")
}

func TestSearchWeb_AnEmptyQueryIsSentBack(t *testing.T) {
	f, model := newSearchFixture(t, WebSearch{Enabled: true}, searchResult("x"),
		scriptedTurn{stop: "tool_use", uses: []anthropic.ToolBlock{use("s1", searchWebTool, `{"query":"  "}`)}},
		scriptedTurn{text: "What should I look up?", stop: "end_turn"},
	)
	f.ask(t, question("search"))
	require.Empty(t, model.searches)
}

func TestSearchSources_NamesSitesNotLinks(t *testing.T) {
	got := searchSources([]anthropic.WebSource{
		{URL: "https://www.a.pt/x", Title: "A"},
		{URL: "not a url"},
		{URL: "https://b.pt/y", Title: "B"},
		{URL: "https://c.pt/z", Title: "C"},
		{URL: "https://d.pt/w", Title: "D"},
	})
	require.Equal(t, []map[string]string{
		{"site": "a.pt", "title": "A"},
		{"site": "b.pt", "title": "B"},
		{"site": "c.pt", "title": "C"},
	}, got)
}
