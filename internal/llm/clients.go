package llm

import (
	"context"
	"iter"
	"log/slog"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/lib"
	"google.golang.org/genai"
)

// ChatClient abstracts LLM chat capabilities needed by domain services.
type ChatClient interface {
	GenerateResponse(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	GenerateContent(ctx context.Context, prompt, apiKey string, config *genai.GenerateContentConfig) (string, error)
	GenerateContentStream(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error)
	GenerateContentStreamWithCache(ctx context.Context, prompt string, config *genai.GenerateContentConfig, cacheKey string) (iter.Seq2[*genai.GenerateContentResponse, error], error)
	Model() string
}

// EmbeddingClient abstracts embedding operations needed by domain services.
type EmbeddingClient interface {
	GenerateQueryEmbedding(ctx context.Context, query string) ([]float32, error)
	GeneratePOIEmbedding(ctx context.Context, name, description, category string) ([]float32, error)
}

// GeminiChatClient adapts the generativeAI LLM client to the ChatClient interface.
type GeminiChatClient struct {
	client *generativeAI.LLMChatClient
}

// NewGeminiChatClient creates a ChatClient backed by Gemini.
func NewGeminiChatClient(ctx context.Context, apiKey string) (ChatClient, error) {
	client, err := generativeAI.NewLLMChatClient(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	return &GeminiChatClient{client: client}, nil
}

func (g *GeminiChatClient) GenerateResponse(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return g.client.GenerateResponse(ctx, prompt, config)
}

func (g *GeminiChatClient) GenerateContent(ctx context.Context, prompt, apiKey string, config *genai.GenerateContentConfig) (string, error) {
	return g.client.GenerateContent(ctx, prompt, apiKey, config)
}

func (g *GeminiChatClient) GenerateContentStream(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	return g.client.GenerateContentStream(ctx, prompt, config)
}

func (g *GeminiChatClient) GenerateContentStreamWithCache(ctx context.Context, prompt string, config *genai.GenerateContentConfig, cacheKey string) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	return g.client.GenerateContentStreamWithCache(ctx, prompt, config, cacheKey)
}

func (g *GeminiChatClient) Model() string {
	if g.client == nil {
		return ""
	}
	return g.client.ModelName
}

// GeminiEmbeddingClient adapts the generativeAI embedding service.
type GeminiEmbeddingClient struct {
	service *generativeAI.EmbeddingService
}

// NewGeminiEmbeddingClient creates an EmbeddingClient backed by Gemini.
func NewGeminiEmbeddingClient(ctx context.Context, logger *slog.Logger) (EmbeddingClient, error) {
	svc, err := generativeAI.NewEmbeddingService(ctx, logger)
	if err != nil {
		return nil, err
	}
	return &GeminiEmbeddingClient{service: svc}, nil
}

func (g *GeminiEmbeddingClient) GenerateQueryEmbedding(ctx context.Context, query string) ([]float32, error) {
	return g.service.GenerateQueryEmbedding(ctx, query)
}

func (g *GeminiEmbeddingClient) GeneratePOIEmbedding(ctx context.Context, name, description, category string) ([]float32, error) {
	return g.service.GeneratePOIEmbedding(ctx, name, description, category)
}
