package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/FACorreiaa/loci-connect-api/pkg/llmerrors"
)

// defaultToolTurnMaxTokens bounds one model call in a tool loop. These are
// short spoken answers and small tool inputs, not generated plans.
const defaultToolTurnMaxTokens = 1024

// BlockType names what a ToolBlock holds.
type BlockType string

const (
	BlockText             BlockType = "text"
	BlockToolUse          BlockType = "tool_use"
	BlockToolResult       BlockType = "tool_result"
	BlockThinking         BlockType = "thinking"
	BlockRedactedThinking BlockType = "redacted_thinking"
)

// Roles a ToolMessage can have.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// ToolBlock is one piece of a message. It is plain data, without SDK types, so
// a caller can store a conversation as JSON and hand it back.
type ToolBlock struct {
	Type BlockType `json:"type"`

	// BlockText.
	Text string `json:"text,omitempty"`

	// BlockToolUse, and ID alone on BlockToolResult.
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`

	// BlockToolResult.
	Result  string `json:"result,omitempty"`
	IsError bool   `json:"is_error,omitempty"`

	// BlockThinking and BlockRedactedThinking, replayed unchanged.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

// ToolMessage is one turn of a conversation.
type ToolMessage struct {
	Role   string      `json:"role"`
	Blocks []ToolBlock `json:"blocks"`
}

// ToolDef describes one tool to the model. Tools are sent strict, so Required
// must name every key in Properties and the API validates the input against
// them: a handler receives input of the declared shape.
type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
}

// ToolTurnRequest is one model call in a loop the caller owns.
type ToolTurnRequest struct {
	System   string
	Tools    []ToolDef
	Messages []ToolMessage
	// MaxTokens zero gets defaultToolTurnMaxTokens.
	MaxTokens int64
	// AdaptiveThinking leaves thinking on. Off by default for latency: the
	// answer is read aloud. With it on, Message carries thinking blocks that
	// the caller must send back unchanged, and history must stay append-only.
	AdaptiveThinking bool
	// FirstEventTimeout zero means the context is the only bound.
	FirstEventTimeout time.Duration
	// ForbidTools makes the model answer with what it has. The tools are still
	// sent, because a history with tool calls in it needs them defined.
	ForbidTools bool
}

// ToolUsage is what one call spent.
type ToolUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// ToolTurnResult is what one call produced.
type ToolTurnResult struct {
	// Message is the assistant turn, in the order the model wrote it. Check
	// StopReason before running any tool in it: on max_tokens the last tool
	// input may be cut off.
	Message ToolMessage
	// StopReason is the API's own value. A refusal is reported here rather
	// than as an error, because there is no other provider to fail over to
	// with tools: the caller decides what to tell the user.
	StopReason string
	Model      string
	// FirstEvent is how long the model took to start answering. A new set of
	// strict tool schemas has to compile first, so the first call can be slow.
	FirstEvent time.Duration
	Usage      ToolUsage
}

// ToolUses returns the tool calls in the turn.
func (r *ToolTurnResult) ToolUses() []ToolBlock {
	var uses []ToolBlock
	for _, b := range r.Message.Blocks {
		if b.Type == BlockToolUse {
			uses = append(uses, b)
		}
	}
	return uses
}

// ToolTurn streams one model call with tools. onText, if set, receives each
// piece of text as it arrives. On an error, any text already delivered stands:
// the caller knows, from having received it, whether anything was said.
//
// This sits beside the chat chain rather than in it. lib.ChatClient is a single
// prompt string, and no other provider here has tool support, so a caller of
// this method handles failure itself.
func (c *ChatClient) ToolTurn(
	ctx context.Context,
	req ToolTurnRequest,
	onText func(string),
) (*ToolTurnResult, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	// NewStreaming blocks until the response headers, so the watchdog has to
	// be running before it is called.
	errNoFirstEvent := fmt.Errorf("%w: anthropic sent nothing within %s", llmerrors.ErrUnavailable, req.FirstEventTimeout)
	var watchdog *time.Timer
	if req.FirstEventTimeout > 0 {
		watchdog = time.AfterFunc(req.FirstEventTimeout, func() { cancel(errNoFirstEvent) })
		defer watchdog.Stop()
	}
	failure := func(err error) error {
		if errors.Is(context.Cause(ctx), errNoFirstEvent) {
			return errNoFirstEvent
		}
		return classify(err)
	}

	started := time.Now()
	var firstEvent time.Duration
	stream := c.sdk.Messages.NewStreaming(ctx, c.toolParams(req))
	defer func() { _ = stream.Close() }()
	if err := stream.Err(); err != nil {
		return nil, failure(err)
	}

	var msg sdk.Message
	for stream.Next() {
		if watchdog != nil {
			watchdog.Stop()
		}
		if firstEvent == 0 {
			firstEvent = time.Since(started)
		}
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return nil, fmt.Errorf("%w: anthropic stream: %w", llmerrors.ErrUnavailable, err)
		}
		if onText == nil {
			continue
		}
		delta, ok := event.AsAny().(sdk.ContentBlockDeltaEvent)
		if !ok {
			continue
		}
		if text, ok := delta.Delta.AsAny().(sdk.TextDelta); ok && text.Text != "" {
			onText(text.Text)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, failure(err)
	}
	if msg.StopReason == "" {
		return nil, fmt.Errorf("%w: anthropic stream ended without a stop reason", llmerrors.ErrUnavailable)
	}

	return &ToolTurnResult{
		Message:    ToolMessage{Role: RoleAssistant, Blocks: blocksFromContent(msg.Content)},
		StopReason: string(msg.StopReason),
		Model:      string(msg.Model),
		FirstEvent: firstEvent,
		Usage: ToolUsage{
			InputTokens:      msg.Usage.InputTokens,
			OutputTokens:     msg.Usage.OutputTokens,
			CacheReadTokens:  msg.Usage.CacheReadInputTokens,
			CacheWriteTokens: msg.Usage.CacheCreationInputTokens,
		},
	}, nil
}

func (c *ChatClient) toolParams(req ToolTurnRequest) sdk.MessageNewParams {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultToolTurnMaxTokens
	}

	p := sdk.MessageNewParams{
		Model:     sdk.Model(c.model),
		MaxTokens: maxTokens,
		Messages:  messageParams(req.Messages),
		Tools:     toolParams(req.Tools),
	}
	if req.ForbidTools {
		p.ToolChoice = sdk.ToolChoiceUnionParam{OfNone: &sdk.ToolChoiceNoneParam{}}
	}
	if req.System != "" {
		// A breakpoint on the system block caches the tools ahead of it too.
		// Per-request context belongs in the messages, after this point.
		p.System = []sdk.TextBlockParam{{
			Text:         req.System,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		}}
	}
	if req.AdaptiveThinking {
		p.Thinking = sdk.ThinkingConfigParamUnion{OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{}}
	} else {
		p.Thinking = sdk.ThinkingConfigParamUnion{OfDisabled: &sdk.ThinkingConfigDisabledParam{}}
	}
	if c.effort != "" {
		p.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffort(c.effort)}
	}
	return p
}

func toolParams(defs []ToolDef) []sdk.ToolUnionParam {
	tools := make([]sdk.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		properties := d.Properties
		if properties == nil {
			// Strict schemas need the key present even for a tool with no input.
			properties = map[string]any{}
		}
		tools = append(tools, sdk.ToolUnionParam{OfTool: &sdk.ToolParam{
			Name:        d.Name,
			Description: sdk.String(d.Description),
			Strict:      sdk.Bool(true),
			InputSchema: sdk.ToolInputSchemaParam{
				Properties:  properties,
				Required:    d.Required,
				ExtraFields: map[string]any{"additionalProperties": false},
			},
		}})
	}
	return tools
}

func messageParams(messages []ToolMessage) []sdk.MessageParam {
	params := make([]sdk.MessageParam, 0, len(messages))
	for _, m := range messages {
		blocks := make([]sdk.ContentBlockParamUnion, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockText:
				blocks = append(blocks, sdk.NewTextBlock(b.Text))
			case BlockToolUse:
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, sdk.NewToolUseBlock(b.ToolUseID, input, b.ToolName))
			case BlockToolResult:
				blocks = append(blocks, sdk.NewToolResultBlock(b.ToolUseID, b.Result, b.IsError))
			case BlockThinking:
				blocks = append(blocks, sdk.NewThinkingBlock(b.Signature, b.Thinking))
			case BlockRedactedThinking:
				blocks = append(blocks, sdk.NewRedactedThinkingBlock(b.Data))
			}
		}
		if m.Role == RoleAssistant {
			params = append(params, sdk.NewAssistantMessage(blocks...))
		} else {
			params = append(params, sdk.NewUserMessage(blocks...))
		}
	}
	return params
}

// blocksFromContent keeps what can be replayed. Block types added later are
// skipped by type, never by position.
func blocksFromContent(content []sdk.ContentBlockUnion) []ToolBlock {
	blocks := make([]ToolBlock, 0, len(content))
	for _, block := range content {
		switch b := block.AsAny().(type) {
		case sdk.TextBlock:
			if b.Text != "" {
				blocks = append(blocks, ToolBlock{Type: BlockText, Text: b.Text})
			}
		case sdk.ToolUseBlock:
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			blocks = append(blocks, ToolBlock{Type: BlockToolUse, ToolUseID: b.ID, ToolName: b.Name, Input: input})
		case sdk.ThinkingBlock:
			blocks = append(blocks, ToolBlock{Type: BlockThinking, Thinking: b.Thinking, Signature: b.Signature})
		case sdk.RedactedThinkingBlock:
			blocks = append(blocks, ToolBlock{Type: BlockRedactedThinking, Data: b.Data})
		}
	}
	return blocks
}
