package service

import (
	"context"
	"iter"

	"github.com/FACorreiaa/loci-connect-api/internal/llm"
	"google.golang.org/genai"
)

// TestLLMClient wraps the mock to satisfy the concrete type requirement
type TestLLMClient struct {
	GenerateContentStreamWithCacheFn func(ctx context.Context, prompt string, config *genai.GenerateContentConfig, cacheKey string) (iter.Seq2[*genai.GenerateContentResponse, error], error)
	GenerateContentStreamFn          func(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error)
	GenerateResponseFn               func(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	GenerateContentFn                func(ctx context.Context, prompt, apiKey string, config *genai.GenerateContentConfig) (string, error)
	ModelFn                          func() string
}

func (t *TestLLMClient) GenerateContentStreamWithCache(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
	cacheKey string,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	if t.GenerateContentStreamWithCacheFn != nil {
		return t.GenerateContentStreamWithCacheFn(ctx, prompt, config, cacheKey)
	}
	return nil, nil
}

func (t *TestLLMClient) GenerateContentStream(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	if t.GenerateContentStreamFn != nil {
		return t.GenerateContentStreamFn(ctx, prompt, config)
	}
	return nil, nil
}

func (t *TestLLMClient) GenerateResponse(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	if t.GenerateResponseFn != nil {
		return t.GenerateResponseFn(ctx, prompt, config)
	}
	return nil, nil
}

func (t *TestLLMClient) GenerateContent(ctx context.Context, prompt, apiKey string, config *genai.GenerateContentConfig) (string, error) {
	if t.GenerateContentFn != nil {
		return t.GenerateContentFn(ctx, prompt, apiKey, config)
	}
	return "", nil
}

func (t *TestLLMClient) Model() string {
	if t.ModelFn != nil {
		return t.ModelFn()
	}
	return ""
}

var _ llm.ChatClient = (*TestLLMClient)(nil)
