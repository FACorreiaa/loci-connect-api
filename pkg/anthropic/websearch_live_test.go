package anthropic

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveWebSearch answers one question before hotel comparison's web search
// is switched on: does the model in use accept the web search tool next to a
// strict tool, and what does a searched answer cost?
//
// It talks to Anthropic and runs up to two searches (a few cents), so it is
// opt-in, and it is the only test here that takes a real key. Use the key from
// the haiku-trial workspace.
//
//	ANTHROPIC_LIVE_API_KEY=sk-ant-... go test ./pkg/anthropic/ -run TestLiveWebSearch -v
//
// ANTHROPIC_LIVE_MODEL overrides the model (default claude-haiku-5-5).
//
// Pass: set HOTEL_COMPARE_WEB_SEARCH=true. Fail with a 400 naming the tool:
// leave it false; reports are then written without the web.
func TestLiveWebSearch(t *testing.T) {
	key := os.Getenv("ANTHROPIC_LIVE_API_KEY")
	if key == "" {
		t.Skip("set ANTHROPIC_LIVE_API_KEY to run the live web search check")
	}
	model := os.Getenv("ANTHROPIC_LIVE_MODEL")
	if model == "" {
		model = "claude-haiku-5-5"
	}
	client, err := New(Options{APIKey: key, Model: model, Effort: "low", StreamTTL: 90 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := client.ToolTurn(ctx, ToolTurnRequest{
		System: "Search the web once for the question, then call submit_answer. Do not answer in text.",
		Tools: []ToolDef{{
			Name:        "submit_answer",
			Description: "Hand in the answer.",
			Properties: map[string]any{
				"answer":  map[string]any{"type": "string"},
				"sources": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			Required: []string{"answer", "sources"},
		}},
		Messages: []ToolMessage{{Role: RoleUser, Blocks: []ToolBlock{{
			Type: BlockText, Text: "Which Barcelona metro line serves Fontana station?",
		}}}},
		MaxTokens: 1024,
		WebSearch: &WebSearch{MaxUses: 2},
	}, nil)
	if err != nil {
		t.Fatalf("the model refused the web search tool, leave HOTEL_COMPARE_WEB_SEARCH off: %v", err)
	}

	t.Logf("model=%s stop_reason=%s first_event=%s", res.Model, res.StopReason, res.FirstEvent)
	t.Logf("usage: input=%d output=%d cache_read=%d cache_write=%d web_searches=%d",
		res.Usage.InputTokens, res.Usage.OutputTokens, res.Usage.CacheReadTokens, res.Usage.CacheWriteTokens, res.Usage.WebSearchRequests)
	for _, s := range res.WebSources {
		t.Logf("source: %s (%s) %s", s.URL, s.Title, s.PageAge)
	}
	for _, u := range res.ToolUses() {
		t.Logf("tool call %s: %s", u.ToolName, u.Input)
	}

	if res.Usage.WebSearchRequests == 0 || len(res.WebSources) == 0 {
		t.Errorf("no search ran: searches=%d sources=%d", res.Usage.WebSearchRequests, len(res.WebSources))
	}
	if res.StopReason == "pause_turn" {
		t.Errorf("the call paused mid-search; hotel comparison falls back to no search when this happens")
	}
	if uses := res.ToolUses(); len(uses) != 1 || uses[0].ToolName != "submit_answer" {
		t.Errorf("want one submit_answer call after searching, got %+v (stop_reason %s)", uses, res.StopReason)
	}
}
