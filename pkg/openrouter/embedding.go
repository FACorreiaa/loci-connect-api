package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// EmbeddingClient implements the application's embedding interface through
// OpenRouter's embeddings endpoint.
type EmbeddingClient struct {
	transport *ChatClient
	model     string
	dimension int
}

var _ generativeAI.EmbeddingClient = (*EmbeddingClient)(nil)

func NewEmbeddingClient(cfg config.AIConfig, logger *slog.Logger) (*EmbeddingClient, error) {
	if cfg.EmbeddingModel == "" {
		return nil, errors.New("OpenRouter embedding model is required")
	}
	if cfg.EmbeddingDimension <= 0 {
		return nil, errors.New("OpenRouter embedding dimension must be positive")
	}
	transport, err := NewChatClient(cfg, logger)
	if err != nil {
		return nil, err
	}
	return &EmbeddingClient{
		transport: transport,
		model:     cfg.EmbeddingModel,
		dimension: cfg.EmbeddingDimension,
	}, nil
}

func (c *EmbeddingClient) GenerateQueryEmbedding(ctx context.Context, query string) ([]float32, error) {
	return c.generateOne(ctx, query)
}

func (c *EmbeddingClient) GeneratePOIEmbedding(
	ctx context.Context,
	name string,
	description string,
	category string,
) ([]float32, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("poi name cannot be empty")
	}
	text := fmt.Sprintf("Name: %s\nCategory: %s", name, category)
	if description != "" {
		text += "\nDescription: " + description
	}
	return c.generateOne(ctx, text)
}

func (c *EmbeddingClient) GenerateCityEmbedding(
	ctx context.Context,
	name string,
	country string,
	description string,
) ([]float32, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("city name cannot be empty")
	}
	text := fmt.Sprintf("City: %s, Country: %s", name, country)
	if description != "" {
		text += "\nDescription: " + description
	}
	return c.generateOne(ctx, text)
}

func (c *EmbeddingClient) GenerateUserPreferenceEmbedding(
	ctx context.Context,
	interests []string,
	preferences map[string]string,
) ([]float32, error) {
	var builder strings.Builder
	builder.WriteString("User Interests: ")
	builder.WriteString(strings.Join(interests, ", "))
	if len(preferences) > 0 {
		builder.WriteString("\nPreferences: ")
		keys := make([]string, 0, len(preferences))
		for key := range preferences {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&builder, "%s: %s; ", key, preferences[key])
		}
	}
	return c.generateOne(ctx, builder.String())
}

func (c *EmbeddingClient) BatchGenerateEmbeddings(
	ctx context.Context,
	texts []string,
) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, errors.New("no texts provided for batch embedding")
	}
	for index, value := range texts {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("embedding text at index %d cannot be empty", index)
		}
	}

	ctx, cancel := withOptionalTimeout(ctx, c.transport.generateTTL)
	defer cancel()
	response, err := c.sendEmbeddingRequest(ctx, embeddingRequest{
		Model:      c.model,
		Input:      texts,
		Dimensions: c.dimension,
	})
	if err != nil {
		return nil, llmerrors.Classify(err)
	}

	embeddings := make([][]float32, len(texts))
	for _, item := range response.Data {
		if item.Index < 0 || item.Index >= len(embeddings) {
			return nil, fmt.Errorf("OpenRouter returned invalid embedding index %d", item.Index)
		}
		if len(item.Embedding) != c.dimension {
			return nil, fmt.Errorf(
				"OpenRouter returned embedding dimension %d, want %d",
				len(item.Embedding),
				c.dimension,
			)
		}
		embeddings[item.Index] = item.Embedding
	}
	for index, embedding := range embeddings {
		if len(embedding) == 0 {
			return nil, fmt.Errorf("OpenRouter omitted embedding at index %d", index)
		}
	}
	return embeddings, nil
}

func (c *EmbeddingClient) Close() {}

func (c *EmbeddingClient) generateOne(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("embedding text cannot be empty")
	}
	embeddings, err := c.BatchGenerateEmbeddings(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return embeddings[0], nil
}

type embeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions"`
}

type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

func (c *EmbeddingClient) sendEmbeddingRequest(
	ctx context.Context,
	payload embeddingRequest,
) (*embeddingResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode OpenRouter embedding request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			c.transport.baseURL+"/embeddings",
			bytes.NewReader(body),
		)
		if err != nil {
			return nil, fmt.Errorf("create OpenRouter embedding request: %w", err)
		}
		c.transport.setHeaders(req)

		resp, err := c.transport.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send OpenRouter embedding request: %w", err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			var result embeddingResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return nil, fmt.Errorf("decode OpenRouter embedding response: %w", err)
			}
			return &result, nil
		}

		apiErr := readAPIError(resp)
		if attempt >= c.transport.maxRetries || !isRetryableStatus(resp.StatusCode) {
			return nil, apiErr
		}
		delay := retryDelay(
			resp.Header.Get("Retry-After"),
			c.transport.baseDelay,
			c.transport.maxDelay,
			attempt,
		)
		c.transport.logger.WarnContext(
			ctx,
			"retrying OpenRouter embedding request",
			slog.Int("status", resp.StatusCode),
			slog.Int("attempt", attempt+1),
			slog.Duration("delay", delay),
		)
		if err := waitForRetry(ctx, delay); err != nil {
			return nil, err
		}
	}
}
