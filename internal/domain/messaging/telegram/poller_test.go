package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
)

// cursorRepo implements only what the poller uses. The rest of
// messaging.Repository is exercised in that package.
type cursorRepo struct {
	mu      sync.Mutex
	cursors map[string]int64
}

func newCursorRepo() *cursorRepo { return &cursorRepo{cursors: map[string]int64{}} }

func (r *cursorRepo) key(platform, account string) string { return platform + ":" + account }

func (r *cursorRepo) Cursor(_ context.Context, platform, accountID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cursors[r.key(platform, accountID)], nil
}

func (r *cursorRepo) SetCursor(_ context.Context, platform, accountID string, updateID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(platform, accountID)
	if updateID > r.cursors[k] {
		r.cursors[k] = updateID
	}
	return nil
}

func (r *cursorRepo) LinkForChat(context.Context, string, string) (messaging.Link, error) {
	return messaging.Link{}, messaging.ErrNotLinked
}

func (r *cursorRepo) LinkForUser(context.Context, uuid.UUID, string) (messaging.Link, error) {
	return messaging.Link{}, messaging.ErrNotLinked
}
func (r *cursorRepo) Unlink(context.Context, uuid.UUID, string) error         { return nil }
func (r *cursorRepo) UnlinkChat(context.Context, string, string) error        { return nil }
func (r *cursorRepo) TouchLink(context.Context, string, string, string) error { return nil }
func (r *cursorRepo) CreateCode(context.Context, uuid.UUID, string, []byte, time.Time) error {
	return nil
}

func (r *cursorRepo) RedeemCode(context.Context, string, string, string, []byte, time.Time) (messaging.Link, error) {
	return messaging.Link{}, messaging.ErrBadCode
}

// recordingHandler answers everything and remembers what it was asked.
type recordingHandler struct {
	mu   sync.Mutex
	seen []string
}

func (h *recordingHandler) Handle(_ context.Context, in messaging.InboundMessage) (messaging.OutboundMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen = append(h.seen, in.Text)
	return messaging.OutboundMessage{Text: "ok: " + in.Text}, nil
}

func (h *recordingHandler) texts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

func update(id, chatID int64, text string) map[string]any {
	return map[string]any{
		"update_id": id,
		"message": map[string]any{
			"chat": map[string]any{"id": chatID},
			"from": map[string]any{"first_name": "Fernando", "username": "fernando"},
			"text": text,
		},
	}
}

func newPoller(t *testing.T, api *fakeAPI, repo messaging.Repository, handler Handler) *Poller {
	t.Helper()
	api.reply["getMe"] = map[string]any{"id": 987654, "username": "loci_bot"}
	return NewPoller(api.client(), repo, handler, nil)
}

// runOnce starts the poller, waits for it to work through one batch, and stops.
func runOnce(t *testing.T, p *Poller) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = p.Run(ctx)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			cancel()
			<-done
			return
		case <-time.After(20 * time.Millisecond):
			if got, _ := p.repo.Cursor(context.Background(), messaging.PlatformTelegram, "987654"); got > 0 {
				cancel()
				<-done
				return
			}
		}
	}
}

func TestAMessageIsAnsweredAndAcknowledged(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getUpdates"] = []any{update(10, 555, "three days in Lisbon")}

	repo, handler := newCursorRepo(), &recordingHandler{}
	runOnce(t, newPoller(t, api, repo, handler))

	if got := handler.texts(); len(got) == 0 || got[0] != "three days in Lisbon" {
		t.Fatalf("handler saw %v", got)
	}

	sent := api.callsTo("sendMessage")
	if len(sent) == 0 {
		t.Fatal("no reply was sent")
	}
	if text, _ := sent[0].body["text"].(string); !strings.Contains(text, "three days in Lisbon") {
		t.Errorf("reply = %q", text)
	}

	if got, _ := repo.Cursor(t.Context(), messaging.PlatformTelegram, "987654"); got != 10 {
		t.Errorf("cursor = %d, want 10", got)
	}
}

// The watermark is per bot. A token swapped for another bot starts a new update
// sequence from a low number, and inheriting the old watermark would compare
// every new message as already seen and drop it — silently, permanently, and
// with the bot looking healthy in every other respect.
func TestADifferentBotStartsItsOwnSequence(t *testing.T) {
	repo := newCursorRepo()

	// The old bot got a long way through its stream.
	if err := repo.SetCursor(t.Context(), messaging.PlatformTelegram, "111111", 900_000); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	api := newFakeAPI(t)
	api.reply["getUpdates"] = []any{update(3, 555, "hello from the new bot")}

	handler := &recordingHandler{}
	runOnce(t, newPoller(t, api, repo, handler))

	if got := handler.texts(); len(got) == 0 {
		t.Fatal("the new bot's message was dropped as though it were a redelivery")
	}
	if got, _ := repo.Cursor(t.Context(), messaging.PlatformTelegram, "111111"); got != 900_000 {
		t.Errorf("the old bot's watermark moved to %d", got)
	}
}

// The watermark must only ever go up, or everything after a stale
// acknowledgement is answered twice.
func TestTheWatermarkNeverGoesBackwards(t *testing.T) {
	repo := newCursorRepo()
	ctx := t.Context()

	for _, id := range []int64{50, 20, 10} {
		if err := repo.SetCursor(ctx, messaging.PlatformTelegram, "987654", id); err != nil {
			t.Fatalf("set cursor: %v", err)
		}
	}
	if got, _ := repo.Cursor(ctx, messaging.PlatformTelegram, "987654"); got != 50 {
		t.Errorf("cursor = %d, want it held at 50", got)
	}
}

// The next poll asks for one past the watermark. Asking for the watermark
// itself would redeliver the last message forever.
func TestThePollAsksForWhatItHasNotSeen(t *testing.T) {
	repo := newCursorRepo()
	if err := repo.SetCursor(t.Context(), messaging.PlatformTelegram, "987654", 10); err != nil {
		t.Fatalf("set cursor: %v", err)
	}

	api := newFakeAPI(t)
	api.reply["getUpdates"] = []any{update(11, 555, "next one")}

	runOnce(t, newPoller(t, api, repo, &recordingHandler{}))

	calls := api.callsTo("getUpdates")
	if len(calls) == 0 {
		t.Fatal("no poll was made")
	}
	if offset, _ := calls[0].body["offset"].(float64); offset != 11 {
		t.Errorf("offset = %v, want 11", calls[0].body["offset"])
	}
}

// Photos, stickers and joins have nothing to answer, but the cursor must still
// move past them or the poll returns them forever.
func TestAnUnanswerableUpdateStillAdvancesTheCursor(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getUpdates"] = []any{
		map[string]any{"update_id": 7, "message": map[string]any{"chat": map[string]any{"id": 555}}},
	}

	repo, handler := newCursorRepo(), &recordingHandler{}
	runOnce(t, newPoller(t, api, repo, handler))

	if len(handler.texts()) != 0 {
		t.Error("an update with no text reached the handler")
	}
	if got, _ := repo.Cursor(t.Context(), messaging.PlatformTelegram, "987654"); got != 7 {
		t.Errorf("cursor = %d, want 7", got)
	}
}

// Two processes on one token would both answer every message.
func TestAConflictStopsThePoller(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getMe"] = map[string]any{"id": 987654, "username": "loci_bot"}
	// getMe succeeds; every getUpdates conflicts, which is what a second
	// process polling the same token looks like.
	api.failMethod("getUpdates", 409)

	p := NewPoller(api.client(), newCursorRepo(), &recordingHandler{}, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- p.Run(ctx) }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("the poller stopped without reporting the conflict")
		}
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("the poller kept running through a conflict")
	}
}

func TestCancellationIsNotAFailure(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getMe"] = map[string]any{"id": 987654, "username": "loci_bot"}
	api.reply["getUpdates"] = []any{}

	p := NewPoller(api.client(), newCursorRepo(), &recordingHandler{}, nil)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- p.Run(ctx) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("a bot told to stop reported %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the poller did not stop")
	}
}

// HandleAction is unused by these tests; the interface needs it.
func (h *recordingHandler) HandleAction(context.Context, messaging.InboundAction) (messaging.OutboundMessage, error) {
	return messaging.OutboundMessage{}, nil
}
