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
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
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
	got, err := l.streamPartFromLLM(t.Context(), partPlan{Part: partCityData, CacheKey: "test:city_data"},
		"prompt", func(e locitypes.StreamEvent) { events = append(events, e) }, locitypes.DomainGeneral)
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

	// The stream itself no longer writes any cache: an unparseable answer used
	// to be cached here and replayed for five minutes. The write happens in
	// persistGenerations, after the output has been validated.
	if _, ok := l.cache.Get("test:city_data"); ok {
		t.Error("the stream worker wrote to the cache; only the validated write path may")
	}
}

// A replayed answer reaches the client as the same chunk stream a generated
// one does, flagged so the client — and anyone reading the events — can tell
// which layer answered.
func TestReplayCachedPartStreamsTheStoredText(t *testing.T) {
	client := &TestLLMClient{
		GenerateStreamFn: func(context.Context, string, *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
			t.Fatal("provider called on a cache hit")
			return nil, nil
		},
	}
	l := newStreamService(t, client)

	text := strings.Repeat("x", replayChunkSize+7)
	p := partPlan{
		Part:     partCityData,
		CacheKey: "test:hit",
		Hit:      &cachedPart{Text: text, Layer: observability.LLMCacheLayerDB},
	}

	var events []locitypes.StreamEvent
	if err := l.replayCachedPart(t.Context(), p, func(e locitypes.StreamEvent) { events = append(events, e) },
		locitypes.DomainGeneral); err != nil {
		t.Fatalf("replay: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("sent %d chunks for %d bytes, want 2", len(events), len(text))
	}
	var streamed strings.Builder
	for _, e := range events {
		data := chunkData(t, e)
		if data["cache_used"] != true {
			t.Error("replayed chunks are not flagged as cache hits")
		}
		if data["cache_layer"] != observability.LLMCacheLayerDB {
			t.Errorf("cache_layer = %v, want db", data["cache_layer"])
		}
		streamed.WriteString(data["chunk"].(string))
	}
	if streamed.String() != text {
		t.Error("the client did not receive the stored text")
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

	got, err := l.streamPartFromLLM(t.Context(), partPlan{Part: partItinerary},
		"prompt", func(locitypes.StreamEvent) {}, locitypes.DomainItinerary)
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
