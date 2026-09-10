package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
)

const testSecret = "a3f9c1d2e4b5a6c7d8e9f0a1b2c3d4e5"

func newWebhook(t *testing.T, api *fakeAPI, handler Handler) *Webhook {
	t.Helper()
	h := NewWebhook(api.client(), handler, testSecret, nil)
	if h == nil {
		t.Fatal("NewWebhook returned nil with a secret configured")
	}
	return h
}

func deliver(h http.Handler, method, secret string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/webhooks/telegram", bytes.NewReader(body))
	if secret != "" {
		req.Header.Set(secretHeader, secret)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func encoded(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw
}

// waitFor polls until cond holds or the deadline passes. The webhook answers
// after it has responded, so the tests have to wait for the side effect.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for the update to be handled")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// The endpoint is worthless without the secret, so an empty one must not
// produce a handler at all — nil is the signal not to mount the route.
func TestNoSecretMeansNoWebhook(t *testing.T) {
	api := newFakeAPI(t)
	if h := NewWebhook(api.client(), &recordingHandler{}, "", nil); h != nil {
		t.Fatal("a webhook was built with an empty secret")
	}
	if h := NewWebhook(nil, &recordingHandler{}, testSecret, nil); h != nil {
		t.Fatal("a webhook was built with no client")
	}
	if h := NewWebhook(api.client(), nil, testSecret, nil); h != nil {
		t.Fatal("a webhook was built with no handler")
	}
}

// The secret is the whole authentication. A wrong one gets 401 with nothing in
// the body, because anything descriptive tells a prober how close they are.
func TestAWrongSecretIsRefusedWithAnEmptyBody(t *testing.T) {
	api := newFakeAPI(t)
	handler := &recordingHandler{}
	h := newWebhook(t, api, handler)
	body := encoded(t, update(1, 555, "three days in Lisbon"))

	for name, secret := range map[string]string{
		"missing": "",
		"wrong":   "not-the-secret",
		"prefix":  testSecret[:len(testSecret)-1],
		"longer":  testSecret + "x",
	} {
		t.Run(name, func(t *testing.T) {
			rec := deliver(h, http.MethodPost, secret, body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body = %q, want nothing", rec.Body.String())
			}
		})
	}

	time.Sleep(50 * time.Millisecond)
	if got := handler.texts(); len(got) != 0 {
		t.Errorf("an unauthenticated delivery was handled: %v", got)
	}
}

func TestOnlyPostIsAccepted(t *testing.T) {
	api := newFakeAPI(t)
	h := newWebhook(t, api, &recordingHandler{})

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if rec := deliver(h, method, testSecret, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

func TestAnOversizedUpdateIsRefused(t *testing.T) {
	api := newFakeAPI(t)
	handler := &recordingHandler{}
	h := newWebhook(t, api, handler)

	// A valid update padded past the cap, so the size is the only reason to
	// refuse it.
	padded := map[string]any{
		"update_id": 1,
		"message":   map[string]any{"chat": map[string]any{"id": 555}, "text": "hi"},
		"padding":   strings.Repeat("x", maxUpdateBytes),
	}
	rec := deliver(h, http.MethodPost, testSecret, encoded(t, padded))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}

	time.Sleep(50 * time.Millisecond)
	if got := handler.texts(); len(got) != 0 {
		t.Errorf("an oversized delivery was handled: %v", got)
	}
}

func TestSomethingThatIsNotAnUpdateIsRefused(t *testing.T) {
	api := newFakeAPI(t)
	h := newWebhook(t, api, &recordingHandler{})

	if rec := deliver(h, http.MethodPost, testSecret, []byte("<xml/>")); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAValidDeliveryIsAcknowledgedAndAnswered(t *testing.T) {
	api := newFakeAPI(t)
	handler := &recordingHandler{}
	h := newWebhook(t, api, handler)

	rec := deliver(h, http.MethodPost, testSecret, encoded(t, update(7, 555, "three days in Lisbon")))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	waitFor(t, func() bool { return len(handler.texts()) == 1 })
	if got := handler.texts(); got[0] != "three days in Lisbon" {
		t.Errorf("handler saw %v", got)
	}

	waitFor(t, func() bool { return len(api.callsTo("sendMessage")) == 1 })
	sent := api.callsTo("sendMessage")[0]
	if chat, _ := sent.body["chat_id"].(string); chat != "555" {
		t.Errorf("reply went to chat %q", chat)
	}
	if text, _ := sent.body["text"].(string); !strings.Contains(text, "three days in Lisbon") {
		t.Errorf("reply = %q", text)
	}
}

// blockingHandler holds every message until released, so a test can observe
// what the webhook does while an answer is still being worked out.
type blockingHandler struct {
	release chan struct{}
	entered chan struct{}
}

func (h *blockingHandler) Handle(ctx context.Context, in messaging.InboundMessage) (messaging.OutboundMessage, error) {
	h.entered <- struct{}{}
	select {
	case <-h.release:
		return messaging.OutboundMessage{Text: "done: " + in.Text}, nil
	case <-ctx.Done():
		return messaging.OutboundMessage{}, ctx.Err()
	}
}

// Telegram retries anything that is not a prompt 200 and an itinerary takes
// minutes, so the acknowledgement must not wait for the answer.
func TestTheDeliveryIsAcknowledgedBeforeItIsAnswered(t *testing.T) {
	api := newFakeAPI(t)
	handler := &blockingHandler{release: make(chan struct{}), entered: make(chan struct{}, 1)}
	h := newWebhook(t, api, handler)

	rec := deliver(h, http.MethodPost, testSecret, encoded(t, update(8, 555, "a weekend in Porto")))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// The response is written and the handler is still inside Handle.
	select {
	case <-handler.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler was never invoked")
	}
	if len(api.callsTo("sendMessage")) != 0 {
		t.Fatal("a reply was sent before the handler finished")
	}

	close(handler.release)
	waitFor(t, func() bool { return len(api.callsTo("sendMessage")) == 1 })
}

// The request context dies when ServeHTTP returns. The answer must outlive it,
// or every reply would be cancelled a millisecond after the 200.
func TestTheAnswerSurvivesTheRequestEnding(t *testing.T) {
	api := newFakeAPI(t)
	handler := &blockingHandler{release: make(chan struct{}), entered: make(chan struct{}, 1)}
	h := newWebhook(t, api, handler)

	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram",
		bytes.NewReader(encoded(t, update(9, 555, "a week in Madeira")))).WithContext(ctx)
	req.Header.Set(secretHeader, testSecret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	cancel() // what the server does once the response is written

	<-handler.entered
	close(handler.release)

	waitFor(t, func() bool { return len(api.callsTo("sendMessage")) == 1 })
	if text, _ := api.callsTo("sendMessage")[0].body["text"].(string); !strings.Contains(text, "a week in Madeira") {
		t.Errorf("reply = %q; the answer was cut off by the request context", text)
	}
}

// Photos and stickers arrive as updates with no text. They are acknowledged
// and ignored, not answered with an error.
func TestAnUpdateWithNoTextIsAcknowledgedAndIgnored(t *testing.T) {
	api := newFakeAPI(t)
	handler := &recordingHandler{}
	h := newWebhook(t, api, handler)

	sticker := map[string]any{"update_id": 3, "message": map[string]any{"chat": map[string]any{"id": 555}}}
	if rec := deliver(h, http.MethodPost, testSecret, encoded(t, sticker)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	time.Sleep(50 * time.Millisecond)
	if got := handler.texts(); len(got) != 0 {
		t.Errorf("a textless update reached the handler: %v", got)
	}
	if len(api.callsTo("sendMessage")) != 0 {
		t.Error("a textless update was answered")
	}
}

// The body is read through a limit, never whole: a stranger who found the URL
// and the secret still cannot make the process buffer arbitrary input.
func TestTheBodyIsNeverReadPastTheCap(t *testing.T) {
	api := newFakeAPI(t)
	h := newWebhook(t, api, &recordingHandler{})

	counting := &countingReader{r: strings.NewReader(strings.Repeat("x", 4*maxUpdateBytes))}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram", counting)
	req.Header.Set(secretHeader, testSecret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if counting.n > maxUpdateBytes+1 {
		t.Errorf("read %d bytes, more than the cap plus one", counting.n)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

type panickingHandler struct{}

func (panickingHandler) Handle(context.Context, messaging.InboundMessage) (messaging.OutboundMessage, error) {
	panic("a message the handler could not stomach")
}

// The webhook answers on a detached goroutine. A panic there is not a failed
// request; without recovery it is the whole API process. One bad message
// from one chat must not do that.
func TestAPanicWhileAnsweringDoesNotEscapeTheProcess(t *testing.T) {
	api := newFakeAPI(t)
	h := newWebhook(t, api, panickingHandler{})

	rec := deliver(h, http.MethodPost, testSecret, []byte(`{"update_id":1,"message":{"message_id":1,"chat":{"id":42},"text":"hello"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 before handling", rec.Code)
	}
	// Give the goroutine time to panic and recover; if recovery were missing
	// the test binary itself would die here.
	time.Sleep(50 * time.Millisecond)
}
