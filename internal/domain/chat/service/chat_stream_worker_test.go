package service

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/cachestore"
)

// chunkResponse is one streamed provider response carrying text, and
// optionally the answering model and cumulative usage.
func chunkResponse(text, modelVersion string, in, out int32) *genai.GenerateContentResponse {
	resp := &genai.GenerateContentResponse{
		ModelVersion: modelVersion,
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: text}}},
		}},
	}
	if in > 0 || out > 0 {
		resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     in,
			CandidatesTokenCount: out,
			TotalTokenCount:      in + out,
		}
	}
	return resp
}

// chunkData reads the map a chunk event carries.
func chunkData(t *testing.T, e locitypes.StreamEvent) map[string]any {
	t.Helper()
	data, ok := e.Data.(map[string]any)
	if !ok {
		t.Fatalf("event data is %T, want map[string]any", e.Data)
	}
	return data
}

func streamOf(responses ...*genai.GenerateContentResponse) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		for _, r := range responses {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func newStreamService(t *testing.T, client *TestLLMClient) *ServiceImpl {
	t.Helper()
	store, err := cachestore.New(cachestore.Config{}, slog.Default())
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	return &ServiceImpl{
		logger:   slog.Default(),
		aiClient: client,
		cache:    store,
	}
}

// The provider names the model that answered and reports usage on the way; a
// durable cache row and the interaction record both need them, and until now
// both were dropped on the floor.
func TestStreamWorkerCapturesModelVersionAndUsage(t *testing.T) {
	client := &TestLLMClient{
		GenerateStreamFn: func(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
			return streamOf(
				chunkResponse(`{"city": "Fun`, "", 0, 0),
				chunkResponse(`chal"`, "deepseek-v4-flash-2026-05", 120, 8),
				chunkResponse(`}`, "deepseek-v4-flash-2026-05", 120, 11),
			), nil
		},
	}
	l := newStreamService(t, client)

	var events []locitypes.StreamEvent
	got, err := l.streamWorkerWithResponseAndCache(t.Context(), "prompt", "city_data",
		func(e locitypes.StreamEvent) { events = append(events, e) }, locitypes.DomainGeneral, "test:city_data")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	if got.Text != `{"city": "Funchal"}` {
		t.Errorf("Text = %q", got.Text)
	}
	if got.ModelVersion != "deepseek-v4-flash-2026-05" {
		t.Errorf("ModelVersion = %q", got.ModelVersion)
	}
	// Usage is cumulative per chunk: the last report is the total, not a
	// running sum of every report.
	if got.TokensIn != 120 || got.TokensOut != 11 {
		t.Errorf("tokens = %d in / %d out, want 120 / 11", got.TokensIn, got.TokensOut)
	}

	if len(events) != 3 {
		t.Fatalf("sent %d chunk events, want 3", len(events))
	}
	var streamed strings.Builder
	for _, e := range events {
		if e.Type != locitypes.EventTypeChunk {
			t.Errorf("event type %q, want chunk", e.Type)
		}
		if chunkData(t, e)["cache_used"] != false {
			t.Error("a fresh generation was flagged as a cache hit")
		}
		streamed.WriteString(chunkData(t, e)["chunk"].(string))
	}
	if streamed.String() != got.Text {
		t.Errorf("client saw %q, result holds %q", streamed.String(), got.Text)
	}

	// The old retry cache still receives the text.
	if cached, ok := l.cache.Get("test:city_data"); !ok || cached != got.Text {
		t.Errorf("cache holds %v, want the streamed text", cached)
	}
}

// A cache hit generated nothing, so it names no model and spent no tokens;
// the caller must be able to tell the two apart.
func TestStreamWorkerReplaysTheCacheWithoutAModel(t *testing.T) {
	client := &TestLLMClient{
		GenerateStreamFn: func(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
			t.Fatal("provider called on a cache hit")
			return nil, nil
		},
	}
	l := newStreamService(t, client)
	l.cache.Set("test:hit", `{"cached": true}`, 0)

	var events []locitypes.StreamEvent
	got, err := l.streamWorkerWithResponseAndCache(t.Context(), "prompt", "city_data",
		func(e locitypes.StreamEvent) { events = append(events, e) }, locitypes.DomainGeneral, "test:hit")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.Text != `{"cached": true}` || got.ModelVersion != "" || got.TokensIn != 0 || got.TokensOut != 0 {
		t.Errorf("cache hit result = %+v", got)
	}
	if len(events) == 0 || chunkData(t, events[0])["cache_used"] != true {
		t.Error("replayed chunks are not flagged as cache hits")
	}
}

func TestStreamWorkerReportsAProviderFailure(t *testing.T) {
	boom := errors.New("provider down")
	client := &TestLLMClient{
		GenerateStreamFn: func(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
			return nil, boom
		},
	}
	l := newStreamService(t, client)

	got, err := l.streamWorkerWithResponseAndCache(t.Context(), "prompt", "itinerary",
		func(locitypes.StreamEvent) {}, locitypes.DomainItinerary, "")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the provider's", err)
	}
	if got != (streamResult{}) {
		t.Errorf("a failed stream returned %+v", got)
	}
}

// modelFor is the seam the cache key reads the model through. A client that
// routes per request answers for the request; a plain client answers for the
// process; a client with no opinion leaves the configured model.
func TestModelForPrefersTheRoutedModel(t *testing.T) {
	l := &ServiceImpl{aiClient: &routedClient{model: "per-request-model"}, model: "configured"}
	if got := l.modelFor(t.Context()); got != "per-request-model" {
		t.Errorf("routed: modelFor = %q", got)
	}

	l = &ServiceImpl{aiClient: &TestLLMClient{ModelFn: func() string { return "client-model" }}, model: "configured"}
	if got := l.modelFor(t.Context()); got != "client-model" {
		t.Errorf("plain client: modelFor = %q", got)
	}

	l = &ServiceImpl{aiClient: &TestLLMClient{}, model: "configured"}
	if got := l.modelFor(t.Context()); got != "configured" {
		t.Errorf("silent client: modelFor = %q", got)
	}
}

// routedClient is a ChatClient that also routes per request.
type routedClient struct {
	*TestLLMClient
	model string
}

func (r *routedClient) ModelFor(context.Context) string { return r.model }
