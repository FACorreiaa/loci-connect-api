package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

func evToolStart(index int, id, name string) string {
	return fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, index, id, name)
}

func evToolInput(index int, partial string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}`, index, partial)
}

func evBlockStop(index int) string {
	return fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index)
}

func evTextStart(index int) string {
	return fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index)
}

func userTurn(text string) []ToolMessage {
	return []ToolMessage{{Role: RoleUser, Blocks: []ToolBlock{{Type: BlockText, Text: text}}}}
}

var lookupTool = ToolDef{
	Name:        "list_trips",
	Description: "List the caller's trips.",
	Properties:  map[string]any{"limit": map[string]any{"type": "integer"}},
	Required:    []string{"limit"},
}

func TestToolTurnRequestShape(t *testing.T) {
	f, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "hi"), evBlockStop(0), evMsgDelta("end_turn", 2), evMsgStop),
		Options{Effort: "low"})

	_, err := client.ToolTurn(context.Background(), ToolTurnRequest{
		System:   "You are Loci.",
		Tools:    []ToolDef{lookupTool},
		Messages: userTurn("where am I going?"),
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
	if _, ok := body["tool_choice"]; ok {
		t.Errorf("sent tool_choice, want the default auto")
	}
	if got := body["max_tokens"]; got != float64(defaultToolTurnMaxTokens) {
		t.Errorf("max_tokens = %v", got)
	}
	if got := body["thinking"].(map[string]any)["type"]; got != "disabled" {
		t.Errorf("thinking = %v, want disabled by default", got)
	}
	if got := body["output_config"].(map[string]any)["effort"]; got != "low" {
		t.Errorf("effort = %v", got)
	}

	system := body["system"].([]any)[0].(map[string]any)
	if system["cache_control"] == nil {
		t.Errorf("system block has no cache breakpoint: %v", system)
	}

	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["strict"] != true {
		t.Errorf("tool is not strict: %v", tool)
	}
	schema := tool["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Errorf("schema allows extra properties: %v", schema)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v", schema["type"])
	}
	if _, ok := tool["eager_input_streaming"]; ok {
		t.Errorf("eager input streaming would switch off the API's validation of the input")
	}
}

func TestToolTurnAdaptiveThinkingIsOptIn(t *testing.T) {
	f, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "hi"), evBlockStop(0), evMsgDelta("end_turn", 2), evMsgStop),
		Options{})
	_, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("x"), AdaptiveThinking: true}, nil)
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}
	if got := f.body(t)["thinking"].(map[string]any)["type"]; got != "adaptive" {
		t.Errorf("thinking = %v, want adaptive", got)
	}
}

func TestToolTurnForbidToolsKeepsThemDefined(t *testing.T) {
	f, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "hi"), evBlockStop(0), evMsgDelta("end_turn", 2), evMsgStop),
		Options{})
	_, err := client.ToolTurn(context.Background(), ToolTurnRequest{
		Messages: userTurn("x"), Tools: []ToolDef{lookupTool}, ForbidTools: true,
	}, nil)
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}
	body := f.body(t)
	if got := body["tool_choice"].(map[string]any)["type"]; got != "none" {
		t.Errorf("tool_choice = %v", got)
	}
	if len(body["tools"].([]any)) != 1 {
		t.Errorf("tools must stay defined while the history has tool calls in it")
	}
}

func TestToolTurnStreamsTextAndReportsUsage(t *testing.T) {
	_, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "Lisbon "), evText(0, "on Friday."), evBlockStop(0), evMsgDelta("end_turn", 6), evMsgStop),
		Options{})

	var got []string
	res, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("when?")},
		func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}
	if strings.Join(got, "|") != "Lisbon |on Friday." {
		t.Errorf("deltas = %q", got)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop reason = %q", res.StopReason)
	}
	if res.Model != "claude-haiku-5-5" {
		t.Errorf("model = %q", res.Model)
	}
	if len(res.ToolUses()) != 0 {
		t.Errorf("tool uses = %v", res.ToolUses())
	}
	if len(res.Message.Blocks) != 1 || res.Message.Blocks[0].Text != "Lisbon on Friday." {
		t.Errorf("message = %+v", res.Message)
	}
	if res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 6 || res.Usage.CacheReadTokens != 90 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestToolTurnReportsHowLongTheFirstEventTook(t *testing.T) {
	_, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "Yes."), evBlockStop(0), evMsgDelta("end_turn", 2), evMsgStop), Options{})

	res, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("when?")}, nil)
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}
	if res.FirstEvent <= 0 {
		t.Errorf("first event = %s, want it measured", res.FirstEvent)
	}
}

func TestToolTurnAccumulatesParallelToolInput(t *testing.T) {
	_, client := newTestClient(t, sse(evStart,
		evTextStart(0), evText(0, "Checking."), evBlockStop(0),
		evToolStart(1, "toolu_1", "list_trips"), evToolInput(1, `{"lim`), evToolInput(1, `it":5}`), evBlockStop(1),
		evToolStart(2, "toolu_2", "list_favorites"), evBlockStop(2),
		evMsgDelta("tool_use", 20), evMsgStop), Options{})

	res, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("x"), Tools: []ToolDef{lookupTool}}, nil)
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}
	if res.StopReason != "tool_use" {
		t.Errorf("stop reason = %q", res.StopReason)
	}
	uses := res.ToolUses()
	if len(uses) != 2 {
		t.Fatalf("tool uses = %+v", uses)
	}
	if uses[0].ToolUseID != "toolu_1" || uses[0].ToolName != "list_trips" {
		t.Errorf("first use = %+v", uses[0])
	}
	var in struct{ Limit int }
	if err := json.Unmarshal(uses[0].Input, &in); err != nil || in.Limit != 5 {
		t.Errorf("first input = %s (%v)", uses[0].Input, err)
	}
	if !json.Valid(uses[1].Input) {
		t.Errorf("a tool with no input must still have valid input JSON: %q", uses[1].Input)
	}
}

// The turn after a tool call: every block of the assistant turn goes back as it
// came, and all the results share one user message.
func TestToolTurnReplaysHistory(t *testing.T) {
	f, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "ok"), evBlockStop(0), evMsgDelta("end_turn", 1), evMsgStop), Options{})

	messages := append(userTurn("x"),
		ToolMessage{Role: RoleAssistant, Blocks: []ToolBlock{
			{Type: BlockThinking, Thinking: "hm", Signature: "sig"},
			{Type: BlockText, Text: "Checking."},
			{Type: BlockToolUse, ToolUseID: "toolu_1", ToolName: "list_trips", Input: json.RawMessage(`{"limit":5}`)},
			{Type: BlockToolUse, ToolUseID: "toolu_2", ToolName: "list_favorites"},
		}},
		ToolMessage{Role: RoleUser, Blocks: []ToolBlock{
			{Type: BlockToolResult, ToolUseID: "toolu_1", Result: "[]"},
			{Type: BlockToolResult, ToolUseID: "toolu_2", Result: "denied", IsError: true},
		}},
	)
	if _, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: messages}, nil); err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}

	sent := f.body(t)["messages"].([]any)
	if len(sent) != 3 {
		t.Fatalf("messages = %v", sent)
	}
	assistant := sent[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Errorf("role = %v", assistant["role"])
	}
	blocks := assistant["content"].([]any)
	if blocks[0].(map[string]any)["signature"] != "sig" {
		t.Errorf("thinking block not replayed unchanged: %v", blocks[0])
	}
	use := blocks[2].(map[string]any)
	if use["id"] != "toolu_1" || use["input"].(map[string]any)["limit"] != float64(5) {
		t.Errorf("tool_use = %v", use)
	}
	if empty := blocks[3].(map[string]any)["input"]; empty == nil {
		t.Errorf("a tool_use must send input even when empty")
	}

	results := sent[2].(map[string]any)["content"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %v", results)
	}
	if r := results[1].(map[string]any); r["tool_use_id"] != "toolu_2" || r["is_error"] != true {
		t.Errorf("result = %v", r)
	}
}

func TestToolMessageSurvivesJSON(t *testing.T) {
	in := ToolMessage{Role: RoleAssistant, Blocks: []ToolBlock{
		{Type: BlockText, Text: "a"},
		{Type: BlockToolUse, ToolUseID: "t", ToolName: "n", Input: json.RawMessage(`{"a":1}`)},
	}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ToolMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Blocks[1].ToolName != "n" || string(out.Blocks[1].Input) != `{"a":1}` {
		t.Errorf("round trip = %+v", out)
	}
}

// A refusal is an answer of sorts, not a provider failure: there is nothing to
// fail over to with tools, so the loop has to see it.
func TestToolTurnReportsRefusalAsAStopReason(t *testing.T) {
	_, client := newTestClient(t, sse(evStart, evMsgDelta("refusal", 1), evMsgStop), Options{})
	res, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("x")}, nil)
	if err != nil {
		t.Fatalf("ToolTurn: %v", err)
	}
	if res.StopReason != "refusal" {
		t.Errorf("stop reason = %q", res.StopReason)
	}
}

func TestToolTurnErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  canned
		want error
	}{
		{"spend cap", apiError(http.StatusBadRequest, "invalid_request_error", "You have reached your specified API usage limits"), llmerrors.ErrOutOfCredits},
		{"rate limit", apiError(http.StatusTooManyRequests, "rate_limit_error", "slow down"), llmerrors.ErrRateLimited},
		{"bad key", apiError(http.StatusUnauthorized, "authentication_error", "invalid x-api-key"), llmerrors.ErrAuthFailed},
		{"server", apiError(http.StatusInternalServerError, "api_error", "boom"), llmerrors.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newTestClient(t, tc.res, Options{})
			_, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("x")}, nil)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A stream that dies part way is a failure, but the text delivered before it
// stays delivered.
func TestToolTurnMidStreamFailure(t *testing.T) {
	overloaded := `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	_, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "Lisbon "), overloaded), Options{})

	var got strings.Builder
	_, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("x")},
		func(s string) { got.WriteString(s) })
	if !errors.Is(err, llmerrors.ErrUnavailable) {
		t.Errorf("err = %v, want unavailable", err)
	}
	if got.String() != "Lisbon " {
		t.Errorf("delivered = %q", got.String())
	}
}

func TestToolTurnStreamCutOffWithoutStopReason(t *testing.T) {
	_, client := newTestClient(t, sse(evStart, evTextStart(0), evText(0, "Lis")), Options{})
	_, err := client.ToolTurn(context.Background(), ToolTurnRequest{Messages: userTurn("x")}, nil)
	if !errors.Is(err, llmerrors.ErrUnavailable) {
		t.Errorf("err = %v, want unavailable", err)
	}
}

func TestToolTurnCancellationIsNotAProviderFailure(t *testing.T) {
	_, client := newTestClient(t, sse(evStart, evMsgStop), Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.ToolTurn(ctx, ToolTurnRequest{Messages: userTurn("x")}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// hangingClient talks to a server that answers only once its handler returns,
// which is what a stalled provider looks like.
func hangingClient(t *testing.T, sendHeaders bool) *ChatClient {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if sendHeaders {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	client, err := New(Options{
		APIKey: "sk-ant-test", Model: config.DefaultAnthropicModel,
		BaseURL: srv.URL, HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestToolTurnFirstEventTimeout(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sendHeaders bool
	}{
		{"nothing at all", false},
		{"headers but no events", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := hangingClient(t, tc.sendHeaders)
			start := time.Now()
			_, err := client.ToolTurn(context.Background(), ToolTurnRequest{
				Messages:          userTurn("x"),
				FirstEventTimeout: 50 * time.Millisecond,
			}, nil)
			if !errors.Is(err, llmerrors.ErrUnavailable) {
				t.Errorf("err = %v, want unavailable", err)
			}
			if errors.Is(err, context.Canceled) {
				t.Errorf("a stalled provider must not read as the caller cancelling: %v", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("took %s", elapsed)
			}
		})
	}
}

func TestToolParamsSendEmptyPropertiesForAToolWithNoInput(t *testing.T) {
	raw, err := json.Marshal(toolParams([]ToolDef{{Name: "list_favorites", Description: "d"}}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"properties":{}`) {
		t.Errorf("schema = %s", raw)
	}
}
