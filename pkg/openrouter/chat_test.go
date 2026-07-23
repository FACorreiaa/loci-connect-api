package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

func testAIConfig() config.AIConfig {
	return config.AIConfig{
		Provider:           config.AIProviderOpenRouter,
		APIKey:             "test-key",
		Model:              "openrouter/auto",
		EmbeddingModel:     "google/gemini-embedding-001",
		EmbeddingDimension: 3,
		MaxRetries:         0,
		GenerateTimeout:    time.Second,
		StreamTimeout:      time.Second,
	}
}

func TestChatClientGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got != "https://loci.dev" {
			t.Errorf("HTTP-Referer = %q", got)
		}
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "openrouter/auto" || request.Stream {
			t.Errorf("unexpected request: %+v", request)
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "system" {
			t.Errorf("messages = %+v", request.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"req-1","model":"routed-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
	}))
	defer server.Close()

	client, err := NewChatClient(testAIConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	response, err := client.Generate(context.Background(), "hi", &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText("be concise", genai.RoleUser),
		Temperature:       genai.Ptr[float32](0.2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Text(); got != "hello" {
		t.Fatalf("response text = %q", got)
	}
	if response.UsageMetadata.TotalTokenCount != 5 {
		t.Fatalf("total tokens = %d", response.UsageMetadata.TotalTokenCount)
	}
}

func TestChatClientGenerateStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"req-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"req-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := NewChatClient(testAIConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	sequence, err := client.GenerateStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for response, streamErr := range sequence {
		if streamErr != nil {
			t.Fatal(streamErr)
		}
		output.WriteString(response.Text())
	}
	if got := output.String(); got != "hello" {
		t.Fatalf("stream output = %q", got)
	}
}

func TestChatClientDoesNotRetryPaymentRequired(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"insufficient credits"}}`)
	}))
	defer server.Close()

	cfg := testAIConfig()
	cfg.MaxRetries = 3
	client, err := NewChatClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	_, err = client.Generate(context.Background(), "hi", nil)
	if err == nil {
		t.Fatal("Generate returned nil error")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}
