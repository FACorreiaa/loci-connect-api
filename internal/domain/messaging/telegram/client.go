// Package telegram is the Bot API adapter: it turns Telegram updates into
// messaging.InboundMessage and sends the replies back.
//
// Two delivery modes, chosen by configuration: long polling (Poller), which
// needs no public address and is right for a laptop or a tailnet, and a
// webhook (Webhook), which needs a public HTTPS address and is right for
// production. Both hand each update to the same bridge, so the service above
// cannot tell them apart.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultAPIBase is Telegram's Bot API.
const defaultAPIBase = "https://api.telegram.org"

// maxMessageChars is Telegram's own limit on a message. Longer replies are
// split rather than truncated: an itinerary cut off at 4096 characters is worse
// than one delivered in two parts.
const maxMessageChars = 4096

// ErrConflict means another process is polling the same bot.
//
// Worth its own error: it is what a second deployment against one token looks
// like, and the fix is to stop one of them rather than to retry.
var ErrConflict = errors.New("telegram: another process is receiving this bot's updates")

// Client talks to the Bot API.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// NewClient builds a client for one bot token.
//
// The HTTP client has no overall timeout: getUpdates is a long poll that is
// meant to hang for up to a minute, and a timeout short enough for sendMessage
// would abort it constantly. Each call bounds itself with a context instead.
func NewClient(token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{token: strings.TrimSpace(token), baseURL: defaultAPIBase, http: httpClient}
}

// WithBaseURL points the client at another Bot API address.
//
// For tests, and for a deployment that reaches Telegram through a proxy. A
// field rather than a package variable because tests run in parallel: a global
// one is a data race between a test restoring it and another test's poller
// still reading it.
func (c *Client) WithBaseURL(base string) *Client {
	c.baseURL = strings.TrimRight(base, "/")
	return c
}

// Account identifies the bot a token belongs to.
type Account struct {
	ID       string
	Username string
}

// GetMe reports which bot this token is for.
//
// Called at startup, because the answer is what scopes the delivery watermark.
// A token swapped for another bot starts a new update sequence, and reusing the
// old watermark would silently discard every message the new bot receives.
func (c *Client) GetMe(ctx context.Context) (Account, error) {
	var result struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := c.call(ctx, "getMe", nil, &result); err != nil {
		return Account{}, err
	}
	return Account{ID: fmt.Sprint(result.ID), Username: result.Username}, nil
}

// WebhookInfo is what Telegram believes about where to deliver updates.
type WebhookInfo struct {
	// URL is empty when no webhook is registered, which means Telegram is
	// holding updates for getUpdates instead.
	URL                string `json:"url"`
	PendingUpdateCount int    `json:"pending_update_count"`
	// LastErrorDate and LastErrorMessage are Telegram's own record of the most
	// recent delivery that failed. Unix seconds; zero and empty when none has.
	LastErrorDate    int64  `json:"last_error_date"`
	LastErrorMessage string `json:"last_error_message"`
}

// Set reports whether a webhook is registered.
func (w WebhookInfo) Set() bool { return w.URL != "" }

// GetWebhookInfo asks Telegram how it is delivering this bot's updates.
//
// The two modes are mutually exclusive at Telegram's end: while a webhook is
// registered, getUpdates is refused. This is how a deployment finds out that
// its poller is failing because of a webhook somebody forgot to delete, or
// that its webhook never receives anything because nobody called setWebhook.
func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var info WebhookInfo
	if err := c.call(ctx, "getWebhookInfo", nil, &info); err != nil {
		return WebhookInfo{}, err
	}
	return info, nil
}

// Recording is a voice note or a round video message.
//
// Duration and FileSize are the reason this is decoded at all: they are what
// let a recording that is too long be refused from the update itself, before
// anything is downloaded or sent to a model. Refusing afterwards costs a
// download and a transcription for an answer nobody gets.
type Recording struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MIMEType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// Update is one entry from the bot's update stream.
type Update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		From *struct {
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Text string `json:"text"`
		// Caption is what a recording sent with a note carries. Telegram puts
		// nothing in Text for those, so without this the note is lost.
		Caption string `json:"caption"`
		// Voice is a voice note: the "hold to record" bubble, always Opus in
		// an Ogg container.
		Voice *Recording `json:"voice"`
		// VideoNote is the round video message. It carries no mime_type — it
		// is always MP4 — and is capped more tightly than a voice note
		// because it is billed as video; see config.VoiceConfig.
		VideoNote *Recording `json:"video_note"`
	} `json:"message"`
}

// GetUpdates long-polls for messages from offset onwards.
//
// timeout is how long Telegram holds the request open when there is nothing to
// send, which is what makes this cheap: an idle bot makes one request a minute
// rather than sixty.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	// Only messages. Asking for every update type would deliver edits,
	// reactions and channel posts that this adapter has no handling for, and
	// each one would still have to be acknowledged to move the watermark past.
	body := map[string]any{
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message"},
	}
	if offset > 0 {
		body["offset"] = offset
	}

	var updates []Update
	if err := c.call(ctx, "getUpdates", body, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage delivers text to a chat, splitting it if Telegram will not take
// it in one piece.
func (c *Client) SendMessage(ctx context.Context, chatID, text string) error {
	for _, part := range SplitMessage(text, maxMessageChars) {
		body := map[string]any{"chat_id": chatID, "text": part}
		if err := c.call(ctx, "sendMessage", body, nil); err != nil {
			return err
		}
	}
	return nil
}

// SendTyping shows the typing indicator.
//
// Best effort and deliberately unchecked by callers: an itinerary takes long
// enough that a silent chat looks broken, but failing to say "typing" is not a
// reason to fail the answer.
func (c *Client) SendTyping(ctx context.Context, chatID string) {
	c.SendAction(ctx, chatID, "typing")
}

// SendAction shows one of Telegram's activity indicators.
//
// "record_voice" is the one worth having beyond typing: synthesising a spoken
// reply adds several seconds after the written answer has already arrived, and
// the indicator is the only thing saying that wait is deliberate rather than
// the bot having stopped.
func (c *Client) SendAction(ctx context.Context, chatID, action string) {
	_ = c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

// call performs one Bot API method.
//
// Every error from here is written by hand rather than wrapped, because the
// token is in the URL path: a wrapped *url.Error prints the URL it failed on,
// and the bot's credential would end up in logs the first time the network
// blipped.
func (c *Client) call(ctx context.Context, method string, body map[string]any, out any) error {
	if c.token == "" {
		return errors.New("telegram: no bot token configured")
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("telegram: could not encode a %s request", method)
		}
		payload = bytes.NewReader(encoded)
	}

	endpoint := fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return fmt.Errorf("telegram: could not build a %s request", method)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Deliberately not wrapped: see the comment above.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("telegram: %s could not be reached", method)
	}
	defer func() { _ = resp.Body.Close() }()

	return c.decodeEnvelope(resp, method, out)
}

// decodeEnvelope reads one Bot API response.
//
// Shared by call and by the multipart upload path rather than written twice:
// the response shape is the same whichever way the request was sent, and so
// are the three rules about it — bounded read, 409 named rather than retried,
// and the description sanitised before it can reach a log.
func (c *Client) decodeEnvelope(resp *http.Response, method string, out any) error {
	// Bounded: this is somebody else's server, and an unbounded read from it is
	// a way to run out of memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("telegram: could not read the %s response", method)
	}

	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("telegram: %s returned something that is not a Bot API response", method)
	}

	if !envelope.OK {
		// 409 is a second poller on the same token. It never resolves on its
		// own, so it is named rather than retried.
		if resp.StatusCode == http.StatusConflict {
			return ErrConflict
		}
		// The description is Telegram's and safe: it describes the request,
		// never the token, which travelled in the path and not the body.
		return fmt.Errorf("telegram: %s failed: %s", method, sanitise(envelope.Description, c.token))
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("telegram: could not decode the %s result", method)
	}
	return nil
}

// sanitise removes the token from a message, in case it ever appears in one.
//
// Belt and braces: nothing is expected to echo it back, but this is the last
// point before an upstream string reaches a log.
func sanitise(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "[redacted]")
}

// SplitMessage breaks text into pieces Telegram will accept.
//
// It prefers to break at a blank line, then a line, then a space, so an
// itinerary splits between days rather than mid-word. A run with none of those
// is cut at the limit, because the alternative is not sending it.
func SplitMessage(text string, limit int) []string {
	if limit <= 0 {
		return []string{text}
	}
	if len(text) <= limit {
		return []string{text}
	}

	var parts []string
	for len(text) > limit {
		window := text[:limit]

		cut := -1
		for _, sep := range []string{"\n\n", "\n", " "} {
			if idx := strings.LastIndex(window, sep); idx > 0 {
				cut = idx
				break
			}
		}
		if cut <= 0 {
			cut = limit
		}

		parts = append(parts, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}

// chatIDOf renders a numeric chat id the way the rest of the system stores it.
func chatIDOf(id int64) string { return fmt.Sprint(id) }

// displayNameOf is how the sender is addressed, for the settings page.
func displayNameOf(firstName, username string) string {
	switch {
	case username != "":
		return "@" + username
	case firstName != "":
		return firstName
	default:
		return ""
	}
}
