package poi

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"sync"
	"testing"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/google/uuid"
	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// fakeChatClient scripts one outcome per Generate call. Unused interface
// methods return zero values.
type fakeChatClient struct {
	mu      sync.Mutex
	calls   int
	results []fakeGenerateResult
}

type fakeGenerateResult struct {
	text string
	err  error
}

func (f *fakeChatClient) Generate(_ context.Context, _ string, _ *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls
	f.calls++
	if idx >= len(f.results) {
		return nil, errors.New("fakeChatClient: unexpected Generate call")
	}
	r := f.results[idx]
	if r.err != nil {
		return nil, r.err
	}
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: r.text}}},
		}},
	}, nil
}

func (f *fakeChatClient) GenerateText(context.Context, string, *genai.GenerateContentConfig) (string, error) {
	return "", nil
}

func (f *fakeChatClient) GenerateStream(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	return nil, nil
}

func (f *fakeChatClient) Model() string { return "fake-model" }

func (f *fakeChatClient) Close() error { return nil }

func (f *fakeChatClient) StartChatSession(context.Context, *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	return nil, nil
}

func (f *fakeChatClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ generativeAI.ChatClient = (*fakeChatClient)(nil)

// A failed first LLM attempt must not abort the nearby flow: the strict
// second attempt still runs, and exhausting both yields an honest empty
// result rather than an error the stream turns into "unable to find places".
func TestGenerateAndEnrichPOIsRetriesOnLLMError(t *testing.T) {
	const emptyPOIs = `{"points_of_interest": []}`
	const lat, lon, distance = 32.6669, -16.9241, 50000.0

	cases := []struct {
		name      string
		client    *fakeChatClient // nil means no AI client configured
		wantErr   bool
		wantCalls int
	}{
		{
			name: "first attempt errors, second returns an empty list",
			client: &fakeChatClient{results: []fakeGenerateResult{
				{err: errors.New("model overloaded")},
				{text: emptyPOIs},
			}},
			wantCalls: 2,
		},
		{
			name: "both attempts error",
			client: &fakeChatClient{results: []fakeGenerateResult{
				{err: errors.New("model overloaded")},
				{err: errors.New("still overloaded")},
			}},
			wantCalls: 2,
		},
		{
			name:      "no AI client configured fails fast",
			client:    nil,
			wantErr:   true,
			wantCalls: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &ServiceImpl{logger: slog.Default()}
			if tc.client != nil {
				svc.aiClient = tc.client
			}

			pois, err := svc.generateAndEnrichPOIs(context.Background(), uuid.New(), lat, lon, distance)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error when the AI client is missing")
				}
			} else {
				if err != nil {
					t.Fatalf("generateAndEnrichPOIs returned an error after an LLM failure: %v", err)
				}
				if pois == nil || len(pois) != 0 {
					t.Fatalf("expected an empty, non-nil slice, got %#v", pois)
				}
			}

			gotCalls := 0
			if tc.client != nil {
				gotCalls = tc.client.callCount()
			}
			if gotCalls != tc.wantCalls {
				t.Fatalf("Generate called %d times, want %d", gotCalls, tc.wantCalls)
			}
		})
	}
}

// The domain-specific nearby paths share the same retry contract.
func TestEnrichLLMWithRetryContinuesOnError(t *testing.T) {
	svc := &ServiceImpl{logger: slog.Default()}
	calls := 0
	gen := func() (*locitypes.GenAIResponse, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("model overloaded")
		}
		return &locitypes.GenAIResponse{}, nil
	}

	pois, err := svc.enrichLLMWithRetry(context.Background(), 32.6669, -16.9241, 5000, "restaurants", gen)
	if err != nil {
		t.Fatalf("enrichLLMWithRetry returned an error after a transient LLM failure: %v", err)
	}
	if pois == nil || len(pois) != 0 {
		t.Fatalf("expected an empty, non-nil slice, got %#v", pois)
	}
	if calls != maxLLMPOIAttempts {
		t.Fatalf("gen called %d times, want %d", calls, maxLLMPOIAttempts)
	}
}
