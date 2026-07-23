package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddingClientBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Dimensions != 3 {
			t.Errorf("dimensions = %d", request.Dimensions)
		}
		if len(request.Input) != 2 {
			t.Errorf("input count = %d", len(request.Input))
		}
		_, _ = io.WriteString(w, `{"data":[{"index":1,"embedding":[4,5,6]},{"index":0,"embedding":[1,2,3]}]}`)
	}))
	defer server.Close()

	client, err := NewEmbeddingClient(testAIConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client.transport.baseURL = server.URL
	embeddings, err := client.BatchGenerateEmbeddings(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	if embeddings[0][0] != 1 || embeddings[1][0] != 4 {
		t.Fatalf("embeddings returned out of order: %v", embeddings)
	}
}
