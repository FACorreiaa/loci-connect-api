package anthropic

import (
	"context"
	"fmt"
	"testing"
)

var submitTool = ToolDef{
	Name:        "submit_report",
	Description: "Hand in the report.",
	Properties:  map[string]any{"verdict": map[string]any{"type": "string"}},
	Required:    []string{"verdict"},
}

func evSearchResults(index int, results string) string {
	return fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","caller":{"type":"direct"},"content":%s}}`, index, results)
}

func TestToolTurnWebSearchRequestShape(t *testing.T) {
	f, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "ok"), evBlockStop(0), evMsgDelta("end_turn", 2), evMsgStop), Options{})

	_, err := client.ToolTurn(context.Background(), ToolTurnRequest{
		Tools:     []ToolDef{submitTool},
		Messages:  userTurn("compare these"),
		WebSearch: &WebSearch{MaxUses: 3, BlockedDomains: []string{"example.com"}},
	}, nil)
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}

	body := f.body(t)
	for _, banned := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := body[banned]; ok {
			t.Errorf("sent %s, which Haiku 5.5 answers with a 400", banned)
		}
	}
	tools := body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %v", tools)
	}
	search := tools[1].(map[string]any)
	if search["type"] != "web_search_20250305" || search["name"] != "web_search" {
		t.Errorf("search tool = %v", search)
	}
	if search["max_uses"] != float64(3) {
		t.Errorf("max_uses = %v", search["max_uses"])
	}
	if got := search["blocked_domains"].([]any); len(got) != 1 || got[0] != "example.com" {
		t.Errorf("blocked_domains = %v", got)
	}
}

func TestToolTurnWithoutWebSearchSendsNoSearchTool(t *testing.T) {
	f, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "ok"), evBlockStop(0), evMsgDelta("end_turn", 2), evMsgStop), Options{})
	if _, err := client.ToolTurn(context.Background(), ToolTurnRequest{Tools: []ToolDef{submitTool}, Messages: userTurn("hi")}, nil); err != nil {
		t.Fatal(err)
	}
	if tools := f.body(t)["tools"].([]any); len(tools) != 1 {
		t.Errorf("tools = %v, want only the caller's", tools)
	}
}

func TestToolTurnForceTool(t *testing.T) {
	f, client := newTestClient(t, sse(evStart, evToolStart(0, "toolu_1", "submit_report"), evToolInput(0, `{"verdict":"a"}`), evBlockStop(0), evMsgDelta("tool_use", 5), evMsgStop), Options{})
	res, err := client.ToolTurn(context.Background(), ToolTurnRequest{
		Tools: []ToolDef{submitTool}, Messages: userTurn("hi"), ForceTool: "submit_report",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	choice := f.body(t)["tool_choice"].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != "submit_report" {
		t.Errorf("tool_choice = %v", choice)
	}
	if uses := res.ToolUses(); len(uses) != 1 || string(uses[0].Input) != `{"verdict":"a"}` {
		t.Errorf("tool uses = %+v", uses)
	}
}

func TestToolTurnCollectsWebSources(t *testing.T) {
	results := `[{"type":"web_search_result","url":"https://a.example/review","title":"A review","encrypted_content":"x","page_age":"March 2026"},` +
		`{"type":"web_search_result","url":"https://b.example/guide","title":"Gràcia guide","encrypted_content":"y","page_age":""},` +
		`{"type":"web_search_result","url":"https://a.example/review","title":"A review again","encrypted_content":"z","page_age":""}]`
	usage := `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":40,"server_tool_use":{"web_search_requests":2,"web_fetch_requests":0}}}`
	_, client := newTestClient(t, sse(
		evStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}`,
		evToolInput(0, `{"query":"Verdi 254 Barcelona"}`), evBlockStop(0),
		evSearchResults(1, results), evBlockStop(1),
		evToolStart(2, "toolu_1", "submit_report"), evToolInput(2, `{"verdict":"b"}`), evBlockStop(2),
		usage, evMsgStop,
	), Options{})

	res, err := client.ToolTurn(context.Background(), ToolTurnRequest{
		Tools: []ToolDef{submitTool}, Messages: userTurn("compare"), WebSearch: &WebSearch{MaxUses: 3},
	}, nil)
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}
	if len(res.WebSources) != 2 {
		t.Fatalf("sources = %+v, want two without the repeat", res.WebSources)
	}
	if res.WebSources[0].URL != "https://a.example/review" || res.WebSources[0].PageAge != "March 2026" || res.WebSources[1].Title != "Gràcia guide" {
		t.Errorf("sources = %+v", res.WebSources)
	}
	if res.Usage.WebSearchRequests != 2 {
		t.Errorf("web search requests = %d", res.Usage.WebSearchRequests)
	}
	uses := res.ToolUses()
	if len(uses) != 1 || uses[0].ToolName != "submit_report" {
		t.Errorf("only the caller's tool counts as a tool use, got %+v", uses)
	}
}

func TestToolTurnFailedSearchHasNoSources(t *testing.T) {
	_, client := newTestClient(t, sse(
		evStart,
		evSearchResults(0, `{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}`), evBlockStop(0),
		evTextStart(1), evText(1, "done"), evBlockStop(1),
		evMsgDelta("end_turn", 3), evMsgStop,
	), Options{})
	res, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("x"), WebSearch: &WebSearch{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.WebSources) != 0 {
		t.Errorf("sources = %+v", res.WebSources)
	}
}
