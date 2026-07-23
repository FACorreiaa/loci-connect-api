// Package openrouter adapts OpenRouter's OpenAI-compatible API to the
// provider-neutral interfaces used by the application.
package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

const (
	defaultBaseURL = "https://openrouter.ai/api/v1"
	maxErrorBody   = 1 << 20
	maxSSEEvent    = 1 << 20
)

// ChatClient implements generativeAI.ChatClient using OpenRouter.
type ChatClient struct {
	apiKey      string
	model       string
	baseURL     string
	httpClient  *http.Client
	logger      *slog.Logger
	maxRetries  int
	baseDelay   time.Duration
	maxDelay    time.Duration
	generateTTL time.Duration
	streamTTL   time.Duration
}

var _ generativeAI.ChatClient = (*ChatClient)(nil)

// NewChatClient creates an OpenRouter chat client. The HTTP client has no
// global timeout because each operation is bounded by its configured context.
func NewChatClient(cfg config.AIConfig, logger *slog.Logger) (*ChatClient, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("OpenRouter API key is required")
	}
	if cfg.Model == "" {
		return nil, errors.New("OpenRouter model is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &ChatClient{
		apiKey:      cfg.APIKey,
		model:       cfg.Model,
		baseURL:     defaultBaseURL,
		httpClient:  &http.Client{},
		logger:      logger,
		maxRetries:  cfg.MaxRetries,
		baseDelay:   cfg.RetryBaseDelay,
		maxDelay:    cfg.RetryMaxDelay,
		generateTTL: cfg.GenerateTimeout,
		streamTTL:   cfg.StreamTimeout,
	}, nil
}

func (c *ChatClient) Generate(
	ctx context.Context,
	prompt string,
	cfg *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	ctx, cancel := withOptionalTimeout(ctx, c.generateTTL)
	defer cancel()

	payload := newChatRequest(c.model, prompt, cfg, false)
	resp, err := c.sendChatRequest(ctx, payload)
	if err != nil {
		return nil, llmerrors.Classify(err)
	}
	defer resp.Body.Close()

	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode OpenRouter response: %w", err)
	}
	if result.Error != nil {
		return nil, llmerrors.Classify(result.Error.apiError(http.StatusBadGateway))
	}
	return result.toGenAI(), nil
}

func (c *ChatClient) GenerateText(
	ctx context.Context,
	prompt string,
	cfg *genai.GenerateContentConfig,
) (string, error) {
	resp, err := c.Generate(ctx, prompt, cfg)
	if err != nil {
		return "", err
	}
	return resp.Text(), nil
}

func (c *ChatClient) GenerateStream(
	ctx context.Context,
	prompt string,
	cfg *genai.GenerateContentConfig,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	streamCtx, cancel := withOptionalTimeout(ctx, c.streamTTL)
	payload := newChatRequest(c.model, prompt, cfg, true)
	resp, err := c.sendChatRequest(streamCtx, payload)
	if err != nil {
		cancel()
		return nil, llmerrors.Classify(err)
	}

	seq := func(yield func(*genai.GenerateContentResponse, error) bool) {
		defer cancel()
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), maxSSEEvent)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return
			}

			var chunk chatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				yield(nil, fmt.Errorf("decode OpenRouter stream event: %w", err))
				return
			}
			if chunk.Error != nil {
				yield(nil, llmerrors.Classify(chunk.Error.apiError(http.StatusBadGateway)))
				return
			}
			if !yield(chunk.toGenAI(), nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			yield(nil, llmerrors.Classify(fmt.Errorf("read OpenRouter stream: %w", err)))
		}
	}

	return seq, nil
}

// OpenRouter conversations in this application are prompt-assembled, so the
// stateful SDK session API is intentionally unsupported.
func (c *ChatClient) StartChatSession(
	context.Context,
	*genai.GenerateContentConfig,
) (*generativeAI.ChatSession, error) {
	return nil, errors.New("OpenRouter stateful chat sessions are not supported")
}

func (c *ChatClient) Model() string { return c.model }

func (c *ChatClient) Close() error { return nil }

func (c *ChatClient) sendChatRequest(ctx context.Context, payload chatRequest) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode OpenRouter request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			c.baseURL+"/chat/completions",
			bytes.NewReader(body),
		)
		if err != nil {
			return nil, fmt.Errorf("create OpenRouter request: %w", err)
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send OpenRouter request: %w", err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		apiErr := readAPIError(resp)
		if attempt >= c.maxRetries || !isRetryableStatus(resp.StatusCode) {
			return nil, apiErr
		}
		delay := retryDelay(resp.Header.Get("Retry-After"), c.baseDelay, c.maxDelay, attempt)
		c.logger.WarnContext(
			ctx,
			"retrying OpenRouter request",
			slog.Int("status", resp.StatusCode),
			slog.Int("attempt", attempt+1),
			slog.Duration("delay", delay),
		)
		if err := waitForRetry(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func (c *ChatClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://loci.dev")
	req.Header.Set("X-Title", "Loci")
}

type chatRequest struct {
	Model            string          `json:"model"`
	Messages         []chatMessage   `json:"messages"`
	Stream           bool            `json:"stream"`
	Temperature      *float32        `json:"temperature,omitempty"`
	TopP             *float32        `json:"top_p,omitempty"`
	MaxTokens        int32           `json:"max_tokens,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	PresencePenalty  *float32        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float32        `json:"frequency_penalty,omitempty"`
	Seed             *int32          `json:"seed,omitempty"`
	ResponseFormat   *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

func newChatRequest(
	model string,
	prompt string,
	cfg *genai.GenerateContentConfig,
	stream bool,
) chatRequest {
	messages := make([]chatMessage, 0, 2)
	if cfg != nil {
		if system := contentText(cfg.SystemInstruction); system != "" {
			messages = append(messages, chatMessage{Role: "system", Content: system})
		}
	}
	messages = append(messages, chatMessage{Role: "user", Content: prompt})

	request := chatRequest{Model: model, Messages: messages, Stream: stream}
	if cfg == nil {
		return request
	}
	request.Temperature = cfg.Temperature
	request.TopP = cfg.TopP
	request.MaxTokens = cfg.MaxOutputTokens
	request.Stop = cfg.StopSequences
	request.PresencePenalty = cfg.PresencePenalty
	request.FrequencyPenalty = cfg.FrequencyPenalty
	request.Seed = cfg.Seed
	if cfg.ResponseMIMEType == "application/json" {
		request.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	return request
}

func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

type chatResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []chatChoice   `json:"choices"`
	Usage   usage          `json:"usage"`
	Error   *providerError `json:"error,omitempty"`
}

type providerError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e providerError) apiError(defaultCode int) genai.APIError {
	code := e.Code
	if code == 0 {
		code = defaultCode
	}
	return genai.APIError{Code: code, Message: e.Message}
}

type chatChoice struct {
	Index        int32       `json:"index"`
	Message      chatMessage `json:"message"`
	Delta        chatMessage `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type usage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
}

func (r chatResponse) toGenAI() *genai.GenerateContentResponse {
	candidates := make([]*genai.Candidate, 0, len(r.Choices))
	for _, choice := range r.Choices {
		text := choice.Message.Content
		if text == "" {
			text = choice.Delta.Content
		}
		candidate := &genai.Candidate{
			Index: choice.Index,
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: text}},
			},
		}
		if choice.FinishReason != nil {
			candidate.FinishReason = mapFinishReason(*choice.FinishReason)
		}
		candidates = append(candidates, candidate)
	}

	return &genai.GenerateContentResponse{
		ResponseID:   r.ID,
		ModelVersion: r.Model,
		Candidates:   candidates,
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     r.Usage.PromptTokens,
			CandidatesTokenCount: r.Usage.CompletionTokens,
			TotalTokenCount:      r.Usage.TotalTokens,
		},
	}
}

func mapFinishReason(reason string) genai.FinishReason {
	switch reason {
	case "stop":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	case "":
		return genai.FinishReasonUnspecified
	default:
		return genai.FinishReasonOther
	}
}

func readAPIError(resp *http.Response) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return genai.APIError{Code: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}

	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return genai.APIError{Code: resp.StatusCode, Message: message}
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func retryDelay(retryAfter string, baseDelay, maxDelay time.Duration, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		delay := time.Duration(seconds) * time.Second
		if maxDelay <= 0 || delay <= maxDelay {
			return delay
		}
	}
	delay := baseDelay
	for range attempt {
		delay *= 2
		if maxDelay > 0 && delay >= maxDelay {
			return maxDelay
		}
	}
	return delay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func withOptionalTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
