package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456:AAF-secret-bot-token"

// fakeAPI stands in for api.telegram.org, recording what was asked of it.
//
// Guarded by a mutex because the poller drives it from its own goroutine.
type fakeAPI struct {
	server *httptest.Server

	mu     sync.Mutex
	calls  []call
	reply  map[string]any
	status int
	ok     bool
	desc   string
	// failures overrides the outcome for one method, so getMe can succeed
	// while getUpdates conflicts — which is what a second poller looks like.
	failures map[string]int
}

type call struct {
	method string
	body   map[string]any
	path   string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{ok: true, status: http.StatusOK, reply: map[string]any{}, failures: map[string]int{}}

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		method := parts[len(parts)-1]

		var body map[string]any
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}

		f.mu.Lock()
		f.calls = append(f.calls, call{method: method, body: body, path: r.URL.Path})
		status, ok, desc := f.status, f.ok, f.desc
		if failure, present := f.failures[method]; present {
			status, ok = failure, false
		}
		result, hasResult := f.reply[method]
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		payload := map[string]any{"ok": ok, "description": desc}
		if hasResult {
			payload["result"] = result
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(f.server.Close)

	return f
}

// client returns a client pointed at this fake.
func (f *fakeAPI) client() *Client {
	return NewClient(testToken, nil).WithBaseURL(f.server.URL)
}

func (f *fakeAPI) failMethod(method string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = status
}

func (f *fakeAPI) callsTo(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []call
	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestGetMeReportsWhichBotTheTokenIsFor(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getMe"] = map[string]any{"id": 987654, "username": "loci_bot"}

	account, err := api.client().GetMe(t.Context())
	if err != nil {
		t.Fatalf("getMe: %v", err)
	}
	if account.ID != "987654" || account.Username != "loci_bot" {
		t.Errorf("account = %+v", account)
	}
}

// The token is in the URL path, so a wrapped *url.Error would print it. This is
// the test that keeps the bot's credential out of the logs.
func TestNoFailureEverNamesTheToken(t *testing.T) {
	t.Run("api error", func(t *testing.T) {
		api := newFakeAPI(t)
		api.ok = false
		api.status = http.StatusBadRequest
		// Telegram would not normally echo the token, but if it ever did.
		api.desc = "Unauthorized: token " + testToken + " is not valid"

		err := api.client().SendMessage(t.Context(), "555", "hello")
		if err == nil {
			t.Fatal("no error")
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("the error names the token: %q", err)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		// Nothing listening.
		client := NewClient(testToken, nil).WithBaseURL("http://127.0.0.1:1")

		err := client.SendMessage(t.Context(), "555", "hello")
		if err == nil {
			t.Fatal("no error")
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("the error names the token: %q", err)
		}
	})
}

// A second deployment on one token never resolves by retrying, and both would
// answer every message.
func TestAConflictIsRecognised(t *testing.T) {
	api := newFakeAPI(t)
	api.ok = false
	api.status = http.StatusConflict
	api.desc = "Conflict: terminated by other getUpdates request"

	_, err := api.client().GetUpdates(t.Context(), 0, time.Second)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

// Asking for every update type would deliver edits, reactions and channel posts
// this adapter has no handling for.
func TestOnlyMessagesAreRequested(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getUpdates"] = []any{}

	if _, err := api.client().GetUpdates(t.Context(), 42, 50*time.Second); err != nil {
		t.Fatalf("getUpdates: %v", err)
	}

	calls := api.callsTo("getUpdates")
	if len(calls) != 1 {
		t.Fatalf("%d calls, want 1", len(calls))
	}
	allowed, _ := calls[0].body["allowed_updates"].([]any)
	if len(allowed) != 1 || allowed[0] != "message" {
		t.Errorf("allowed_updates = %v", calls[0].body["allowed_updates"])
	}
	if calls[0].body["offset"] != float64(42) {
		t.Errorf("offset = %v, want 42", calls[0].body["offset"])
	}
}

// An offset of zero means "whatever you have"; sending offset=0 explicitly is
// the same thing, but omitting it keeps the request honest about intent.
func TestAZeroOffsetIsOmitted(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getUpdates"] = []any{}

	if _, err := api.client().GetUpdates(t.Context(), 0, time.Second); err != nil {
		t.Fatalf("getUpdates: %v", err)
	}
	if _, present := api.callsTo("getUpdates")[0].body["offset"]; present {
		t.Error("offset was sent for a bot that has read nothing")
	}
}

// An itinerary cut off at 4096 characters is worse than one in two parts.
func TestALongReplyIsSplitRatherThanTruncated(t *testing.T) {
	api := newFakeAPI(t)

	long := strings.Repeat("Day one: walk the Alfama.\n\n", 400)
	if err := api.client().SendMessage(t.Context(), "555", long); err != nil {
		t.Fatalf("send: %v", err)
	}

	calls := api.callsTo("sendMessage")
	if len(calls) < 2 {
		t.Fatalf("%d messages sent, want the reply split", len(calls))
	}

	var rebuilt strings.Builder
	for _, c := range calls {
		text, _ := c.body["text"].(string)
		if len(text) > maxMessageChars {
			t.Fatalf("a part is %d characters, over the limit", len(text))
		}
		rebuilt.WriteString(text)
	}
	if !strings.Contains(rebuilt.String(), "Alfama") {
		t.Error("the content did not survive splitting")
	}
}

func TestSplitMessagePrefersABreakOverCuttingAWord(t *testing.T) {
	text := strings.Repeat("a", 30) + "\n\n" + strings.Repeat("b", 30)

	parts := SplitMessage(text, 40)
	if len(parts) != 2 {
		t.Fatalf("%d parts, want 2", len(parts))
	}
	if strings.Contains(parts[0], "b") {
		t.Errorf("the split ran past the blank line: %q", parts[0])
	}
}

func TestSplitMessageLeavesShortTextAlone(t *testing.T) {
	parts := SplitMessage("three days in Lisbon", maxMessageChars)
	if len(parts) != 1 || parts[0] != "three days in Lisbon" {
		t.Errorf("parts = %q", parts)
	}
}

// A run with no break at all still has to be sent.
func TestSplitMessageCutsWhenThereIsNowhereToBreak(t *testing.T) {
	parts := SplitMessage(strings.Repeat("x", 100), 40)
	if len(parts) != 3 {
		t.Fatalf("%d parts, want 3", len(parts))
	}
	for _, part := range parts {
		if len(part) > 40 {
			t.Fatalf("a part is %d characters, over the limit", len(part))
		}
	}
}

func TestAnEmptyTokenIsRefusedWithoutACall(t *testing.T) {
	api := newFakeAPI(t)

	empty := NewClient("", nil).WithBaseURL(api.server.URL)
	if err := empty.SendMessage(context.Background(), "555", "hello"); err == nil {
		t.Fatal("a message was sent with no token")
	}
	if len(api.calls) != 0 {
		t.Error("a request was made with no token")
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"@fernando": displayNameOf("Fernando", "fernando"),
		"Fernando":  displayNameOf("Fernando", ""),
		"":          displayNameOf("", ""),
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("display name = %q, want %q", got, want)
		}
	}
}

// Telegram refuses getUpdates while a webhook is registered, so knowing which
// mode Telegram is in is the fastest answer to "why is nothing arriving".
func TestGetWebhookInfoReportsTelegramsDeliveryMode(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getWebhookInfo"] = map[string]any{
		"url":                  "https://api.lociai.fyi/webhooks/telegram",
		"pending_update_count": 3,
		"last_error_date":      1_700_000_000,
		"last_error_message":   "Wrong response from the webhook: 401 Unauthorized",
	}

	info, err := api.client().GetWebhookInfo(t.Context())
	if err != nil {
		t.Fatalf("getWebhookInfo: %v", err)
	}
	if !info.Set() || info.URL != "https://api.lociai.fyi/webhooks/telegram" {
		t.Errorf("info = %+v", info)
	}
	if info.PendingUpdateCount != 3 || info.LastErrorDate != 1_700_000_000 || !strings.Contains(info.LastErrorMessage, "401") {
		t.Errorf("info = %+v", info)
	}

	api.reply["getWebhookInfo"] = map[string]any{"url": ""}
	info, err = api.client().GetWebhookInfo(t.Context())
	if err != nil {
		t.Fatalf("getWebhookInfo: %v", err)
	}
	if info.Set() {
		t.Error("an empty url was reported as a registered webhook")
	}
}
