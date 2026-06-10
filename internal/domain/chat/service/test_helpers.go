package service

import (
	"context"
	"iter"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"google.golang.org/genai"
)

// TestLLMClient wraps the mock to satisfy the ChatClient interface.
type TestLLMClient struct {
	GenerateFn       func(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	GenerateTextFn   func(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (string, error)
	GenerateStreamFn func(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (iter.Seq2[*genai.GenerateContentResponse, error], error)
	ModelFn          func() string
	CloseFn          func() error
}

func (t *TestLLMClient) StartChatSession(ctx context.Context, config *genai.GenerateContentConfig) (*generativeAI.ChatSession, error) {
	panic("unimplemented")
}

func (t *TestLLMClient) Generate(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	if t.GenerateFn != nil {
		return t.GenerateFn(ctx, prompt, config)
	}
	return nil, nil
}

func (t *TestLLMClient) GenerateText(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (string, error) {
	if t.GenerateTextFn != nil {
		return t.GenerateTextFn(ctx, prompt, config)
	}
	return "", nil
}

func (t *TestLLMClient) GenerateStream(
	ctx context.Context,
	prompt string,
	config *genai.GenerateContentConfig,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	if t.GenerateStreamFn != nil {
		return t.GenerateStreamFn(ctx, prompt, config)
	}
	return nil, nil
}

func (t *TestLLMClient) Model() string {
	if t.ModelFn != nil {
		return t.ModelFn()
	}
	return ""
}

func (t *TestLLMClient) Close() error {
	if t.CloseFn != nil {
		return t.CloseFn()
	}
	return nil
}

var _ generativeAI.ChatClient = (*TestLLMClient)(nil)