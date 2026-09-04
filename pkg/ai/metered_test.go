package ai

import (
	"context"
	"errors"
	"iter"
	"testing"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"google.golang.org/genai"
)

// stubClient is a ChatClient whose responses the test controls.
type stubClient struct {
	model     string
	responses []*genai.GenerateContentResponse
	err       error
}

func (s *stubClient) Generate(context.Context, string, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.responses[0], nil
}

func (s *stubClient) GenerateText(context.Context, string, *genai.GenerateContentConfig) (string, error) {
	return "", s.err
}

func (s *stubClient) GenerateStream(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	if s.err != nil {
		return nil, s.err
	}
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		for _, r := range s.responses {
			if !yield(r, nil) {
				return
			}
		}
	}, nil
}

func (s *stubClient) Model() string { return s.model }
func (s *stubClient) Close() error  { return nil }
func (s *stubClient) StartChatSession(context.Context, *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return nil, errors.New("not used")
}

func usageResponse(prompt, candidates, thoughts, total int32) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     prompt,
			CandidatesTokenCount: candidates,
			ThoughtsTokenCount:   thoughts,
			TotalTokenCount:      total,
		},
	}
}

type capture struct {
	calls []struct {
		model  string
		method string
		usage  tokenUsage
	}
}

func (c *capture) record(model, method string, u tokenUsage) {
	c.calls = append(c.calls, struct {
		model  string
		method string
		usage  tokenUsage
	}{model, method, u})
}

func TestMeteredGenerateRecordsUsage(t *testing.T) {
	c := &capture{}
	client := newMetered(&stubClient{
		model:     "gemini-test",
		responses: []*genai.GenerateContentResponse{usageResponse(120, 340, 15, 475)},
	}, c.record)

	if _, err := client.Generate(context.Background(), "plan a day in Porto", nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(c.calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(c.calls))
	}
	got := c.calls[0]
	if got.model != "gemini-test" || got.method != methodGenerate {
		t.Fatalf("recorded %s/%s, want gemini-test/%s", got.model, got.method, methodGenerate)
	}
	want := tokenUsage{Prompt: 120, Completion: 340, Thoughts: 15, Total: 475}
	if got.usage != want {
		t.Fatalf("usage = %+v, want %+v", got.usage, want)
	}
}

// Providers report usage cumulatively on every stream chunk, so the meter must
// take the last report rather than summing chunks, which would multiply the
// bill by the number of chunks.
func TestMeteredStreamRecordsFinalCumulativeUsageOnce(t *testing.T) {
	c := &capture{}
	client := newMetered(&stubClient{
		model: "openrouter-test",
		responses: []*genai.GenerateContentResponse{
			usageResponse(100, 10, 0, 110),
			usageResponse(100, 60, 0, 160),
			usageResponse(100, 250, 5, 355),
		},
	}, c.record)

	seq, err := client.GenerateStream(context.Background(), "plan three days in Lisbon", nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	for range seq {
	}

	if len(c.calls) != 1 {
		t.Fatalf("recorded %d calls, want exactly 1 for one stream", len(c.calls))
	}
	want := tokenUsage{Prompt: 100, Completion: 250, Thoughts: 5, Total: 355}
	if c.calls[0].usage != want {
		t.Fatalf("usage = %+v, want the final cumulative report %+v", c.calls[0].usage, want)
	}
	if c.calls[0].method != methodGenerateStream {
		t.Fatalf("method = %s, want %s", c.calls[0].method, methodGenerateStream)
	}
}

// A stream abandoned by the caller still cost tokens, so what was reported up
// to that point must be recorded rather than dropped.
func TestMeteredStreamRecordsWhenCallerStopsEarly(t *testing.T) {
	c := &capture{}
	client := newMetered(&stubClient{
		model: "openrouter-test",
		responses: []*genai.GenerateContentResponse{
			usageResponse(100, 10, 0, 110),
			usageResponse(100, 60, 0, 160),
		},
	}, c.record)

	seq, err := client.GenerateStream(context.Background(), "plan a weekend", nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	for range seq {
		break
	}

	if len(c.calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(c.calls))
	}
	if c.calls[0].usage.Total != 110 {
		t.Fatalf("total = %d, want the 110 reported before the caller stopped", c.calls[0].usage.Total)
	}
}

func TestMeteredRecordsNothingWhenProviderFails(t *testing.T) {
	c := &capture{}
	client := newMetered(&stubClient{model: "gemini-test", err: errors.New("provider down")}, c.record)

	if _, err := client.Generate(context.Background(), "anything", nil); err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if len(c.calls) != 0 {
		t.Fatalf("recorded %d calls for a failed request, want 0", len(c.calls))
	}
}

func TestMeteredPassesModelThrough(t *testing.T) {
	client := newMetered(&stubClient{model: "gemini-test"}, func(string, string, tokenUsage) {})
	if client.Model() != "gemini-test" {
		t.Fatalf("Model() = %q, want gemini-test", client.Model())
	}
}
