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
	"sync"
	"testing"

	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// fakeAPI stands in for api.anthropic.com: it records each request body and
// answers with one canned response.
type fakeAPI struct {
	mu     sync.Mutex
	bodies []map[string]any
}

type canned struct {
	status int
	body   string
	stream bool
}

func newTestClient(t *testing.T, res canned, opts Options) (*fakeAPI, *ChatClient) {
	t.Helper()
	f := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()

		if res.stream {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		status := res.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, res.body)
	}))
	t.Cleanup(srv.Close)

	opts.APIKey = "sk-ant-test"
	if opts.Model == "" {
		opts.Model = config.DefaultAnthropicModel
	}
	opts.BaseURL = srv.URL
	opts.HTTPClient = srv.Client()
	client, err := New(opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return f, client
}

func (f *fakeAPI) body(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 1 {
		t.Fatalf("made %d requests, want 1", len(f.bodies))
	}
	return f.bodies[0]
}

// message renders a complete non-streaming response.
func message(stop string, blocks ...map[string]any) canned {
	b, _ := json.Marshal(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-haiku-5-5",
		"content":     blocks,
		"stop_reason": stop,
		"usage": map[string]any{
			"input_tokens": 12, "output_tokens": 40,
			"cache_read_input_tokens": 100, "cache_creation_input_tokens": 0,
		},
	})
	return canned{body: string(b)}
}

func textBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func thinkingBlock(text string) map[string]any {
	return map[string]any{"type": "thinking", "thinking": text, "signature": "sig"}
}

func apiError(status int, errType, msg string) canned {
	b, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": errType, "message": msg},
	})
	return canned{status: status, body: string(b)}
}

// sse renders events the way the Messages API streams them.
func sse(events ...string) canned {
	var b strings.Builder
	for _, e := range events {
		var probe struct{ Type string }
		_ = json.Unmarshal([]byte(e), &probe)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, e)
	}
	return canned{body: b.String(), stream: true}
}

const (
	evStart         = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":90}}}`
	evThinkingStart = `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`
	evThinking      = `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"the user wants a plan"}}`
	evStop0         = `{"type":"content_block_stop","index":0}`
	evTextStart1    = `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`
	evStop1         = `{"type":"content_block_stop","index":1}`
	evMsgStop       = `{"type":"message_stop"}`
)

func evText(index int, text string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%q}}`, index, text)
}

func evMsgDelta(stop string, out int) string {
	return fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":%d}}`, stop, out)
}

func ptr[T any](v T) *T { return &v }

// Haiku 5.5 answers any sampling parameter with a 400, and every caller here
// sets one. None of them may reach the wire.
func TestGenerateSendsNoSamplingParameters(t *testing.T) {
	f, client := newTestClient(t, message("end_turn", textBlock("Lisbon")), Options{Effort: "low"})

	_, err := client.Generate(t.Context(), "Where should I go?", &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText("You plan trips.", genai.RoleUser),
		Temperature:       ptr(float32(0.7)),
		TopP:              ptr(float32(0.9)),
		TopK:              ptr(float32(40)),
		PresencePenalty:   ptr(float32(0.5)),
		FrequencyPenalty:  ptr(float32(0.5)),
		Seed:              ptr(int32(7)),
		MaxOutputTokens:   4096,
		StopSequences:     []string{"END"},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	body := f.body(t)
	for _, key := range []string{"temperature", "top_p", "top_k", "presence_penalty", "frequency_penalty", "seed"} {
		if _, ok := body[key]; ok {
			t.Errorf("request carries %q", key)
		}
	}
	if body["model"] != "claude-haiku-5-5" {
		t.Errorf("model = %v", body["model"])
	}
	if body["max_tokens"] != float64(4096) {
		t.Errorf("max_tokens = %v", body["max_tokens"])
	}
	if got := fmt.Sprint(body["stop_sequences"]); got != "[END]" {
		t.Errorf("stop_sequences = %s", got)
	}
	if got := fmt.Sprint(body["system"]); !strings.Contains(got, "You plan trips.") {
		t.Errorf("system = %s", got)
	}
	if got := fmt.Sprint(body["thinking"]); got != "map[type:adaptive]" {
		t.Errorf("thinking = %s, want adaptive", got)
	}
	if got := fmt.Sprint(body["output_config"]); got != "map[effort:low]" {
		t.Errorf("output_config = %s", got)
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 || !strings.Contains(fmt.Sprint(messages[0]), "role:user") {
		t.Errorf("messages = %v, want the prompt as the only, user, turn", messages)
	}
}

// Thinking spends from max_tokens, so a small budget gets none, and the
// operator can turn it off outright. No effort configured sends none.
func TestThinkingIsDisabledOnSmallBudgetsOrByConfig(t *testing.T) {
	cases := []struct {
		name     string
		thinking string
		budget   int32
		want     string
	}{
		{"small budget", "", 512, "map[type:disabled]"},
		{"configured off", config.AnthropicThinkingDisabled, 4096, "map[type:disabled]"},
		{"default", "", 0, "map[type:adaptive]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, client := newTestClient(t, message("end_turn", textBlock("ok")), Options{Thinking: tc.thinking})
			if _, err := client.Generate(t.Context(), "hi", &genai.GenerateContentConfig{MaxOutputTokens: tc.budget}); err != nil {
				t.Fatalf("generate: %v", err)
			}
			body := f.body(t)
			if got := fmt.Sprint(body["thinking"]); got != tc.want {
				t.Errorf("thinking = %s, want %s", got, tc.want)
			}
			if _, ok := body["output_config"]; ok {
				t.Errorf("output_config sent with no effort configured: %v", body["output_config"])
			}
			if tc.budget == 0 && body["max_tokens"] != float64(defaultMaxTokens) {
				t.Errorf("max_tokens = %v, want the default", body["max_tokens"])
			}
		})
	}
}

// A response can open with thinking. Only text is the answer, and usage
// counts cached input as prompt.
func TestGenerateReadsTextBlocksOnly(t *testing.T) {
	_, client := newTestClient(t, message("end_turn", thinkingBlock("hmm"), textBlock("Porto")), Options{})

	resp, err := client.Generate(t.Context(), "hi", nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Text() != "Porto" {
		t.Errorf("text = %q", resp.Text())
	}
	if resp.Candidates[0].FinishReason != genai.FinishReasonStop {
		t.Errorf("finish = %q", resp.Candidates[0].FinishReason)
	}
	u := resp.UsageMetadata
	if u.PromptTokenCount != 112 || u.CandidatesTokenCount != 40 || u.CachedContentTokenCount != 100 || u.TotalTokenCount != 152 {
		t.Errorf("usage = %+v", u)
	}
}

// JSON mode has no Messages equivalent short of a schema: it is asked for in
// the system prompt, and a fence the model adds anyway is removed.
func TestJSONModeAsksForBareJSONAndStripsFences(t *testing.T) {
	f, client := newTestClient(t, message("end_turn", textBlock("```json\n{\"city\":\"Lisbon\"}\n```")), Options{})

	text, err := client.GenerateText(t.Context(), "hi", &genai.GenerateContentConfig{ResponseMIMEType: "application/json"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if text != `{"city":"Lisbon"}` {
		t.Errorf("text = %q, want the fence stripped", text)
	}
	if _, ok := f.body(t)["response_format"]; ok {
		t.Error("request carries OpenAI's response_format")
	}
	if got := fmt.Sprint(f.body(t)["system"]); !strings.Contains(got, "JSON only") {
		t.Errorf("system = %s, want the JSON instruction", got)
	}
}

func TestStripFences(t *testing.T) {
	cases := map[string]string{
		"```json\n[1,2]\n```":   "[1,2]",
		"```\n{\"a\":1}\n```\n": `{"a":1}`,
		`{"a":1}`:               `{"a":1}`,
		"```{\"a\":1}```":       `{"a":1}`,
	}
	for in, want := range cases {
		if got := stripFences(in); got != want {
			t.Errorf("stripFences(%q) = %q, want %q", in, got, want)
		}
	}
}

// A refusal is HTTP 200 with no answer. The chain must move on, not return an
// empty itinerary.
func TestRefusalFailsOver(t *testing.T) {
	_, client := newTestClient(t, message("refusal"), Options{})

	_, err := client.Generate(t.Context(), "hi", nil)
	if !errors.Is(err, llmerrors.ErrUnavailable) || !llmerrors.Failover(err) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// Thinking can spend the whole budget before a word is written.
func TestMaxTokensWithNoTextFailsOver(t *testing.T) {
	_, client := newTestClient(t, message("max_tokens", thinkingBlock("long thought")), Options{})

	_, err := client.Generate(t.Context(), "hi", nil)
	if !errors.Is(err, llmerrors.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// With text it is a truncation, reported the way callers already check for.
func TestMaxTokensWithTextIsATruncation(t *testing.T) {
	_, client := newTestClient(t, message("max_tokens", textBlock("[{\"name\":")), Options{})

	resp, err := client.Generate(t.Context(), "hi", nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Candidates[0].FinishReason != genai.FinishReasonMaxTokens {
		t.Errorf("finish = %q, want MAX_TOKENS", resp.Candidates[0].FinishReason)
	}
}

// Every failure fails over; billing and auth also bench the link, which is
// how hitting the trial's spend cap hands traffic back without a deploy.
func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name     string
		res      canned
		want     error
		terminal bool
	}{
		{"workspace usage limit", apiError(400, "invalid_request_error",
			"You have reached your specified workspace API usage limits."), llmerrors.ErrOutOfCredits, true},
		{"credit balance", apiError(400, "invalid_request_error",
			"Your credit balance is too low to access the Anthropic API."), llmerrors.ErrOutOfCredits, true},
		{"payment required", apiError(402, "billing_error", "Payment required"), llmerrors.ErrOutOfCredits, true},
		{"bad key", apiError(401, "authentication_error", "invalid x-api-key"), llmerrors.ErrAuthFailed, true},
		{"forbidden", apiError(403, "permission_error", "no access"), llmerrors.ErrAuthFailed, true},
		{"rate limited", apiError(429, "rate_limit_error", "slow down"), llmerrors.ErrRateLimited, false},
		{"overloaded", apiError(529, "overloaded_error", "Overloaded"), llmerrors.ErrUnavailable, false},
		{"bad request", apiError(400, "invalid_request_error", "temperature is not supported"), llmerrors.ErrUnavailable, false},
		{"unknown model", apiError(404, "not_found_error", "model: claude-haiku-9"), llmerrors.ErrUnavailable, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newTestClient(t, tc.res, Options{})

			_, err := client.Generate(t.Context(), "hi", nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !llmerrors.Failover(err) {
				t.Errorf("%v does not fail over", err)
			}
			if llmerrors.Terminal(err) != tc.terminal {
				t.Errorf("terminal = %v, want %v", !tc.terminal, tc.terminal)
			}
			if strings.Contains(err.Error(), "sk-ant-test") {
				t.Errorf("error leaks the key: %v", err)
			}
		})
	}
}

// A caller who has gone away is not a provider failure.
func TestCancellationDoesNotFailOver(t *testing.T) {
	_, client := newTestClient(t, message("end_turn", textBlock("ok")), Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := client.Generate(ctx, "hi", nil)
	if err == nil || llmerrors.Failover(err) {
		t.Fatalf("err = %v, want a cancellation that does not fail over", err)
	}
}

type streamed struct {
	texts []string
	last  *genai.GenerateContentResponse
	err   error
}

func drain(t *testing.T, client *ChatClient) streamed {
	t.Helper()
	seq, err := client.GenerateStream(t.Context(), "plan Lisbon", nil)
	if err != nil {
		return streamed{err: err}
	}
	var out streamed
	for resp, err := range seq {
		if err != nil {
			out.err = err
			break
		}
		out.last = resp
		if text := resp.Text(); text != "" {
			out.texts = append(out.texts, text)
		}
	}
	return out
}

// Thinking is not forwarded, text is, and the last chunk carries the
// cumulative usage pkg/ai's metering keeps.
func TestStreamForwardsTextOnlyAndEndsWithUsage(t *testing.T) {
	_, client := newTestClient(t, sse(evStart,
		evThinkingStart, evThinking, evStop0,
		evTextStart1, evText(1, "Day 1: "), evText(1, "Alfama."), evStop1,
		evMsgDelta("end_turn", 57), evMsgStop), Options{})

	got := drain(t, client)
	if got.err != nil {
		t.Fatalf("stream: %v", got.err)
	}
	if strings.Join(got.texts, "|") != "Day 1: |Alfama." {
		t.Errorf("texts = %q", got.texts)
	}
	if got.last == nil || got.last.UsageMetadata == nil {
		t.Fatal("last chunk carries no usage")
	}
	if got.last.Text() != "" {
		t.Errorf("usage chunk carries text %q", got.last.Text())
	}
	u := got.last.UsageMetadata
	if u.PromptTokenCount != 100 || u.CandidatesTokenCount != 57 {
		t.Errorf("usage = %+v, want cumulative 100 in / 57 out", u)
	}
	if got.last.Candidates[0].FinishReason != genai.FinishReasonStop {
		t.Errorf("finish = %q", got.last.Candidates[0].FinishReason)
	}
	if got.last.ModelVersion != "claude-haiku-5-5" {
		t.Errorf("model = %q", got.last.ModelVersion)
	}
}

// A refusal arrives after the stream has opened. It is metered, then reported
// as a failover error before any text reaches the caller.
func TestStreamRefusalFailsOver(t *testing.T) {
	_, client := newTestClient(t, sse(evStart, evMsgDelta("refusal", 3), evMsgStop), Options{})

	got := drain(t, client)
	if !errors.Is(got.err, llmerrors.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", got.err)
	}
	if len(got.texts) != 0 {
		t.Errorf("texts = %q", got.texts)
	}
	if got.last == nil || got.last.UsageMetadata == nil {
		t.Error("a refused stream was not metered")
	}
}

func TestStreamMaxTokensWithNoTextFailsOver(t *testing.T) {
	_, client := newTestClient(t, sse(evStart,
		evThinkingStart, evThinking, evStop0,
		evMsgDelta("max_tokens", 1024), evMsgStop), Options{})

	if got := drain(t, client); !errors.Is(got.err, llmerrors.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", got.err)
	}
}

// A rejected request is reported when the stream is opened, so the chain can
// move on before committing to this provider.
func TestStreamRejectedRequestFailsAtOpen(t *testing.T) {
	_, client := newTestClient(t, apiError(400, "invalid_request_error",
		"You have reached your specified workspace API usage limits."), Options{})

	seq, err := client.GenerateStream(t.Context(), "hi", nil)
	if seq != nil || !errors.Is(err, llmerrors.ErrOutOfCredits) {
		t.Fatalf("seq = %v, err = %v; want ErrOutOfCredits at open", seq != nil, err)
	}
}
