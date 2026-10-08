// Package anthropic adapts Anthropic's native Messages API to the
// provider-neutral chat interface used by the application.
//
// It exists for one trial: Claude Haiku put ahead of the configured primary,
// with the existing chain behind it (see pkg/ai NewChatClient). It is a chat
// link only. Anthropic has no embeddings API, so AI_PROVIDER, which also picks
// the embedding client, never names it.
//
// The adapter is shaped by what the newer Claude models refuse rather than by
// what the callers send:
//
//   - temperature, top_p and top_k are never sent. Haiku 5.5 answers any
//     non-default value with a 400, and every caller here sets a temperature.
//     Penalties and seed have no Messages equivalent and are dropped too.
//   - Thinking is on by default and spends from max_tokens. A response may
//     therefore open with thinking blocks, and only text blocks are read.
//   - A refusal arrives as HTTP 200 with stop_reason "refusal". It is reported
//     as ErrUnavailable so the chain answers from the next provider instead of
//     returning an empty answer.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"google.golang.org/genai"

	generativeAI "github.com/FACorreiaa/go-genai-sdk/v2/lib"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

const (
	// defaultMaxTokens is used when a caller names no limit. The Messages API
	// requires one, and thinking spends from it, so it is sized for an answer
	// with room to think rather than for the answer alone.
	defaultMaxTokens = 8192

	// minThinkingTokens is the smallest budget thinking is left on for. Below
	// it the thinking would take the whole budget and leave no answer.
	minThinkingTokens = 1024

	// jsonOnlyInstruction stands in for OpenAI's response_format json_object,
	// which the Messages API has no equivalent of short of a full schema.
	jsonOnlyInstruction = "Respond with JSON only: no prose before or after it, and no markdown code fences."
)

// ChatClient implements generativeAI.ChatClient over the Messages API.
type ChatClient struct {
	sdk         sdk.Client
	model       string
	effort      string
	thinking    string
	logger      *slog.Logger
	generateTTL time.Duration
	streamTTL   time.Duration
}

var _ generativeAI.ChatClient = (*ChatClient)(nil)

// Options describes one Anthropic link.
type Options struct {
	APIKey string
	Model  string

	// Effort is sent as output_config.effort; empty sends nothing.
	Effort string
	// Thinking is config.AnthropicThinkingDisabled to switch thinking off
	// on every request. Anything else leaves it adaptive.
	Thinking string

	// BaseURL overrides api.anthropic.com. Tests only.
	BaseURL string
	// HTTPClient nil gets the SDK's default.
	HTTPClient *http.Client

	Logger *slog.Logger
	// MaxRetries is handed to the SDK, which retries 408, 409, 429 and 5xx
	// itself. Zero means no retries.
	MaxRetries  int
	GenerateTTL time.Duration
	StreamTTL   time.Duration
}

// NewChatClient creates the trial's client from the server's configuration.
// Retries and timeouts are the ones every other link uses.
func NewChatClient(cfg config.AIConfig, logger *slog.Logger) (*ChatClient, error) {
	return New(Options{
		APIKey:      cfg.Anthropic.APIKey,
		Model:       cfg.Anthropic.Model,
		Effort:      cfg.Anthropic.Effort,
		Thinking:    cfg.Anthropic.Thinking,
		Logger:      logger,
		MaxRetries:  cfg.MaxRetries,
		GenerateTTL: cfg.GenerateTimeout,
		StreamTTL:   cfg.StreamTimeout,
	})
}

// New creates a client from explicit options.
func New(opts Options) (*ChatClient, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, errors.New("anthropic API key is required")
	}
	if opts.Model == "" {
		return nil, errors.New("anthropic model is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	reqOpts := []option.RequestOption{
		option.WithAPIKey(opts.APIKey),
		option.WithMaxRetries(max(opts.MaxRetries, 0)),
	}
	if opts.BaseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(opts.BaseURL))
	}
	if opts.HTTPClient != nil {
		reqOpts = append(reqOpts, option.WithHTTPClient(opts.HTTPClient))
	}

	return &ChatClient{
		sdk:         sdk.NewClient(reqOpts...),
		model:       opts.Model,
		effort:      opts.Effort,
		thinking:    opts.Thinking,
		logger:      logger,
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

	var callOpts []option.RequestOption
	if c.generateTTL > 0 {
		// Without an explicit request timeout the SDK refuses a non-streaming
		// call whose max_tokens suggests it could run past ten minutes. The
		// context above is the real bound, so hand the SDK the same one.
		callOpts = append(callOpts, option.WithRequestTimeout(c.generateTTL))
	}

	msg, err := c.sdk.Messages.New(ctx, c.params(prompt, cfg), callOpts...)
	if err != nil {
		return nil, classify(err)
	}
	text := messageText(msg.Content)
	if err := answerError(msg.StopReason, text != ""); err != nil {
		return nil, err
	}
	if jsonMode(cfg) {
		text = stripFences(text)
	}
	return toGenAI(text, msg.StopReason, string(msg.Model), msg.ID, &msg.Usage), nil
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

// GenerateStream yields text as it arrives and nothing else: thinking deltas
// are dropped. The last chunk carries no text, only the stop reason and the
// cumulative usage, which is what pkg/ai's metering keeps.
//
// Fences are not stripped here even in JSON mode: a closing fence cannot be
// told from content until the stream ends. Every streaming caller already
// strips them before parsing, as it must for the OpenRouter models.
func (c *ChatClient) GenerateStream(
	ctx context.Context,
	prompt string,
	cfg *genai.GenerateContentConfig,
) (iter.Seq2[*genai.GenerateContentResponse, error], error) {
	streamCtx, cancel := withOptionalTimeout(ctx, c.streamTTL)
	stream := c.sdk.Messages.NewStreaming(streamCtx, c.params(prompt, cfg))
	// The request has been made by now; a rejected one is reported here,
	// before the caller commits to this provider.
	if err := stream.Err(); err != nil {
		_ = stream.Close()
		cancel()
		return nil, classify(err)
	}

	seq := func(yield func(*genai.GenerateContentResponse, error) bool) {
		defer cancel()
		defer func() { _ = stream.Close() }()

		var (
			msg     sdk.Message
			sawText bool
		)
		for stream.Next() {
			event := stream.Current()
			if err := msg.Accumulate(event); err != nil {
				yield(nil, fmt.Errorf("%w: anthropic stream: %w", llmerrors.ErrUnavailable, err))
				return
			}
			delta, ok := event.AsAny().(sdk.ContentBlockDeltaEvent)
			if !ok {
				continue
			}
			text, ok := delta.Delta.AsAny().(sdk.TextDelta)
			if !ok || text.Text == "" {
				continue
			}
			sawText = true
			if !yield(toGenAI(text.Text, "", "", msg.ID, nil), nil) {
				return
			}
		}
		if err := stream.Err(); err != nil {
			yield(nil, classify(err))
			return
		}

		// Usage before any error, so a refusal or an empty answer is still
		// metered: the tokens were spent whether or not they produced text.
		if !yield(toGenAI("", msg.StopReason, string(msg.Model), msg.ID, &msg.Usage), nil) {
			return
		}
		if err := answerError(msg.StopReason, sawText); err != nil {
			yield(nil, err)
		}
	}
	return seq, nil
}

// Conversations here are prompt-assembled, so the stateful session API is
// unsupported, as it is for the OpenRouter client.
func (c *ChatClient) StartChatSession(
	context.Context,
	*genai.GenerateContentConfig,
) (*generativeAI.ChatSession, error) {
	return nil, errors.New("anthropic stateful chat sessions are not supported")
}

func (c *ChatClient) Model() string { return c.model }

func (c *ChatClient) Close() error { return nil }

// params maps the Gemini-shaped config onto a Messages request. Only the
// system instruction, output budget and stop sequences cross over; see the
// package comment for what is dropped and why.
func (c *ChatClient) params(prompt string, cfg *genai.GenerateContentConfig) sdk.MessageNewParams {
	maxTokens := int64(defaultMaxTokens)
	var system string
	if cfg != nil {
		if cfg.MaxOutputTokens > 0 {
			maxTokens = int64(cfg.MaxOutputTokens)
		}
		system = contentText(cfg.SystemInstruction)
	}
	if jsonMode(cfg) {
		if system != "" {
			system += "\n\n"
		}
		system += jsonOnlyInstruction
	}

	p := sdk.MessageNewParams{
		Model:     sdk.Model(c.model),
		MaxTokens: maxTokens,
		Messages:  []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(prompt))},
	}
	if system != "" {
		p.System = []sdk.TextBlockParam{{Text: system}}
	}
	if cfg != nil && len(cfg.StopSequences) > 0 {
		p.StopSequences = cfg.StopSequences
	}
	if c.thinking == config.AnthropicThinkingDisabled || maxTokens < minThinkingTokens {
		p.Thinking = sdk.ThinkingConfigParamUnion{OfDisabled: &sdk.ThinkingConfigDisabledParam{}}
	} else {
		p.Thinking = sdk.ThinkingConfigParamUnion{OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{}}
	}
	if c.effort != "" {
		p.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffort(c.effort)}
	}
	return p
}

func jsonMode(cfg *genai.GenerateContentConfig) bool {
	return cfg != nil && cfg.ResponseMIMEType == "application/json"
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

// messageText joins the text blocks of a response. Thinking, redacted
// thinking and anything newer are skipped by type, never by position.
func messageText(content []sdk.ContentBlockUnion) string {
	var text strings.Builder
	for _, block := range content {
		if b, ok := block.AsAny().(sdk.TextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	return text.String()
}

// answerError turns a response that carries no usable answer into an error
// the chain fails over on.
func answerError(reason sdk.StopReason, hasText bool) error {
	switch {
	case reason == sdk.StopReasonRefusal:
		return fmt.Errorf("%w: anthropic declined the request", llmerrors.ErrUnavailable)
	case reason == sdk.StopReasonMaxTokens && !hasText:
		// Thinking spent the whole budget. Another provider, which does not
		// think on this budget, may well answer.
		return fmt.Errorf("%w: anthropic reached max_tokens before writing any text", llmerrors.ErrUnavailable)
	case !hasText:
		return fmt.Errorf("%w: anthropic stopped with %q", llmerrors.ErrEmptyResponse, reason)
	}
	return nil
}

// stripFences removes a markdown code fence wrapped around a whole answer,
// for a model that adds one despite being asked not to.
func stripFences(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return text
	}
	// Drop the opening fence line, language tag and all.
	if nl := strings.IndexByte(trimmed, '\n'); nl >= 0 {
		trimmed = trimmed[nl+1:]
	} else {
		trimmed = strings.TrimPrefix(trimmed, "```")
	}
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func toGenAI(text string, reason sdk.StopReason, model, id string, u *sdk.Usage) *genai.GenerateContentResponse {
	candidate := &genai.Candidate{
		Content: &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{{Text: text}},
		},
	}
	if reason != "" {
		candidate.FinishReason = mapFinishReason(reason)
	}
	resp := &genai.GenerateContentResponse{
		ResponseID:   id,
		ModelVersion: model,
		Candidates:   []*genai.Candidate{candidate},
	}
	if u != nil {
		resp.UsageMetadata = usageMetadata(*u)
	}
	return resp
}

// usageMetadata counts cached input as prompt: the account paid for it, and
// a meter that dropped it would under-report exactly the long prompts.
// Output includes thinking; the API does not report the two apart.
func usageMetadata(u sdk.Usage) *genai.GenerateContentResponseUsageMetadata {
	prompt := int32(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens)
	completion := int32(u.OutputTokens)
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        prompt,
		CandidatesTokenCount:    completion,
		CachedContentTokenCount: int32(u.CacheReadInputTokens),
		TotalTokenCount:         prompt + completion,
	}
}

func mapFinishReason(reason sdk.StopReason) genai.FinishReason {
	switch reason {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
		return genai.FinishReasonStop
	case sdk.StopReasonMaxTokens:
		return genai.FinishReasonMaxTokens
	case sdk.StopReasonRefusal:
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonOther
	}
}

// classify puts an API failure in the llmerrors class the chain acts on.
//
// Every failure fails over: that is the trial's contract, so a spent budget
// or a rejected request hands traffic back to the old chain rather than
// failing it. Billing and auth are terminal, so the link cools down instead
// of being retried on every request.
//
// The SDK's own message is not reused. It carries the request line, which is
// noise in a log; the API's message is what says what went wrong.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("%w: anthropic: %w", llmerrors.ErrUnavailable, err)
	}

	status := apiErr.StatusCode
	errType, detail := apiMessage(apiErr)
	if detail == "" {
		detail = http.StatusText(status)
	}
	lower := strings.ToLower(detail)

	var class error
	switch {
	// Anthropic reports a spent balance or a reached workspace limit as a
	// 400, not a 402. Waiting will not fix either, and the next provider is
	// paid from a different account.
	case status == http.StatusPaymentRequired,
		strings.Contains(lower, "credit balance"),
		strings.Contains(lower, "usage limit"):
		class = llmerrors.ErrOutOfCredits
	case status == http.StatusUnauthorized, status == http.StatusForbidden,
		errType == "authentication_error", errType == "permission_error":
		class = llmerrors.ErrAuthFailed
	case status == http.StatusTooManyRequests, errType == "rate_limit_error":
		class = llmerrors.ErrRateLimited
	default:
		class = llmerrors.ErrUnavailable
	}
	return fmt.Errorf("%w: anthropic returned %d: %s", class, status, detail)
}

// apiMessage reads the error type and message out of the response body.
func apiMessage(apiErr *sdk.Error) (errType, message string) {
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(apiErr.RawJSON()), &body); err != nil {
		return "", ""
	}
	return body.Error.Type, strings.TrimSpace(body.Error.Message)
}

func withOptionalTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
