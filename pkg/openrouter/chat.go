// Package openrouter adapts OpenAI's chat-completions dialect to the
// provider-neutral interfaces used by the application.
//
// Named for the backend it was written against and still defaults to, but the
// dialect is not OpenRouter's: OpenAI, xAI, NVIDIA and a self-hosted Hermes
// gateway all speak it. NewCompatibleChatClient is how one of those is reached;
// see pkg/ai/providers for which addresses are known and which are the user's
// to supply.
package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	// name is the backend this client is pointed at, used in errors and logs.
	// Without it every failure against a user's own gateway would be reported
	// as an OpenRouter failure.
	name        string
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

// Options describes one OpenAI-dialect backend.
//
// It exists so a user's own provider can be built per request, which
// config.AIConfig cannot express: that struct describes the server's single
// configured provider, and a credential someone brought is neither single nor
// the server's.
type Options struct {
	// Name is the backend, for errors and logs. Defaults to "openrouter".
	Name string

	// BaseURL defaults to OpenRouter's. For a user-supplied address it must
	// already have been through providers.ParseGatewayURL.
	BaseURL string

	APIKey string
	Model  string

	// HTTPClient is shared so a per-user provider gets connection reuse rather
	// than a TLS handshake per request. For a user-supplied address this must
	// be a providers.GatewayHTTPClient, which re-checks the target at dial
	// time. Nil gets a plain client, which is only safe for the addresses this
	// build hardcodes.
	HTTPClient *http.Client

	Logger      *slog.Logger
	MaxRetries  int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	GenerateTTL time.Duration
	StreamTTL   time.Duration
}

// NewChatClient creates a chat client for the server's configured provider.
// The HTTP client has no global timeout because each operation is bounded by
// its configured context.
func NewChatClient(cfg config.AIConfig, logger *slog.Logger) (*ChatClient, error) {
	return NewCompatibleChatClient(Options{
		APIKey:      cfg.APIKey,
		Model:       cfg.Model,
		Logger:      logger,
		MaxRetries:  cfg.MaxRetries,
		BaseDelay:   cfg.RetryBaseDelay,
		MaxDelay:    cfg.RetryMaxDelay,
		GenerateTTL: cfg.GenerateTimeout,
		StreamTTL:   cfg.StreamTimeout,
	})
}

// NewCompatibleChatClient creates a client for any backend speaking OpenAI's
// chat-completions dialect.
func NewCompatibleChatClient(opts Options) (*ChatClient, error) {
	name := opts.Name
	if name == "" {
		name = "openrouter"
	}
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if opts.APIKey == "" {
		return nil, fmt.Errorf("%s API key is required", name)
	}
	if opts.Model == "" {
		return nil, fmt.Errorf("%s model is required", name)
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &ChatClient{
		name:        name,
		apiKey:      opts.APIKey,
		model:       opts.Model,
		baseURL:     baseURL,
		httpClient:  httpClient,
		logger:      logger,
		maxRetries:  opts.MaxRetries,
		baseDelay:   opts.BaseDelay,
		maxDelay:    opts.MaxDelay,
		generateTTL: opts.GenerateTTL,
		streamTTL:   opts.StreamTTL,
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
		return nil, fmt.Errorf("decode %s response: %w", c.name, err)
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
				yield(nil, fmt.Errorf("decode %s stream event: %w", c.name, err))
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
			yield(nil, llmerrors.Classify(fmt.Errorf("read %s stream: %w", c.name, err)))
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
	return nil, fmt.Errorf("%s stateful chat sessions are not supported", c.name)
}

func (c *ChatClient) Model() string { return c.model }

func (c *ChatClient) Close() error { return nil }

func (c *ChatClient) sendChatRequest(ctx context.Context, payload chatRequest) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", c.name, err)
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			c.baseURL+"/chat/completions",
			bytes.NewReader(body),
		)
		if err != nil {
			return nil, fmt.Errorf("create %s request: %w", c.name, err)
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send %s request: %w", c.name, err)
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
			"retrying llm request",
			slog.String("provider", c.name),
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
	// Attribution headers OpenRouter reads for its own dashboards. Other
	// backends ignore them, but sending a referrer to a user's private gateway
	// is not ours to do.
	if c.name == "openrouter" {
		req.Header.Set("HTTP-Referer", "https://loci.dev")
		req.Header.Set("X-Title", "Loci")
	}
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
