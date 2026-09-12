package telegram

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
)

// Nothing regresses for the existing callers: a message with no keyboard must
// carry no reply_markup at all, not an empty one.
func TestSendMessageWithoutMarkupIsUnchanged(t *testing.T) {
	api := newFakeAPI(t)

	if err := api.client().SendMessage(t.Context(), "42", "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	calls := api.callsTo("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("%d calls, want 1", len(calls))
	}
	if _, present := calls[0].body["reply_markup"]; present {
		t.Errorf("a plain message carried reply_markup: %v", calls[0].body)
	}
}

// The keyboard goes on the last part of a split message. A button under text
// the reader has not reached yet is worse than no button.
func TestMarkupGoesOnTheLastPartOnly(t *testing.T) {
	api := newFakeAPI(t)

	long := strings.Repeat("Uma frase sobre a Madeira.\n\n", 400)
	markup := &InlineKeyboard{InlineKeyboard: [][]InlineButton{{
		{Text: "Show more places", CallbackData: "p|abc|2|i"},
	}}}

	if err := api.client().SendMessageWithMarkup(t.Context(), "42", long, markup); err != nil {
		t.Fatalf("SendMessageWithMarkup: %v", err)
	}

	calls := api.callsTo("sendMessage")
	if len(calls) < 2 {
		t.Fatalf("%d calls, want the message split across several", len(calls))
	}
	for i, c := range calls {
		_, present := c.body["reply_markup"]
		if want := i == len(calls)-1; present != want {
			t.Errorf("part %d of %d: reply_markup present = %v, want %v", i+1, len(calls), present, want)
		}
	}
}

func TestAnswerCallbackQueryAndEditMarkup(t *testing.T) {
	api := newFakeAPI(t)
	client := api.client()

	client.AnswerCallbackQuery(t.Context(), "q-1", "One moment…")
	calls := api.callsTo("answerCallbackQuery")
	if len(calls) != 1 || calls[0].body["callback_query_id"] != "q-1" || calls[0].body["text"] != "One moment…" {
		t.Errorf("answerCallbackQuery body = %v", calls)
	}

	// Removing a keyboard is a nil markup, not an empty one: Telegram reads an
	// absent reply_markup as "take the buttons away".
	if err := client.EditMessageReplyMarkup(t.Context(), "42", 77, nil); err != nil {
		t.Fatalf("EditMessageReplyMarkup: %v", err)
	}
	edits := api.callsTo("editMessageReplyMarkup")
	if len(edits) != 1 || edits[0].body["message_id"] != float64(77) {
		t.Fatalf("editMessageReplyMarkup body = %v", edits)
	}
	if _, present := edits[0].body["reply_markup"]; present {
		t.Errorf("removing a keyboard still sent one: %v", edits[0].body)
	}
}

// A press arrives with the message it was attached to, which is where the chat
// and message ids come from.
func TestACallbackUpdateDecodes(t *testing.T) {
	api := newFakeAPI(t)
	api.reply["getUpdates"] = []any{
		map[string]any{
			"update_id": 7,
			"callback_query": map[string]any{
				"id":   "q-9",
				"data": "p|abc|2|i",
				"message": map[string]any{
					"message_id": 55,
					"chat":       map[string]any{"id": 4242},
				},
			},
		},
	}

	updates, err := api.client().GetUpdates(t.Context(), 0, time.Second)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("%d updates, want 1", len(updates))
	}

	q := updates[0].CallbackQuery
	if q == nil {
		t.Fatal("the callback query did not decode")
	}
	if q.ID != "q-9" || q.Data != "p|abc|2|i" {
		t.Errorf("callback = %+v", q)
	}
	if q.Message == nil || q.Message.MessageID != 55 || q.Message.Chat.ID != 4242 {
		t.Errorf("the callback's message did not decode: %+v", q.Message)
	}
}

// A token Telegram would reject is dropped rather than truncated: a cut token
// still sends, and then decodes to a different page or to nothing.
func TestAnOversizedTokenIsDroppedNotTruncated(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	oversized := strings.Repeat("x", maxCallbackDataBytes+1)
	if kb := keyboardFor(logger, []messaging.Button{{Label: "More", Data: oversized}}); kb != nil {
		t.Errorf("an oversized token produced a keyboard: %+v", kb)
	}

	kb := keyboardFor(logger, []messaging.Button{
		{Label: "More", Data: oversized},
		{Label: "Fine", Data: "p|abc|2|i"},
	})
	if kb == nil || len(kb.InlineKeyboard) != 1 {
		t.Fatalf("the usable button was lost: %+v", kb)
	}
	if kb.InlineKeyboard[0][0].CallbackData != "p|abc|2|i" {
		t.Errorf("the wrong button survived: %+v", kb.InlineKeyboard[0][0])
	}

	if keyboardFor(logger, nil) != nil {
		t.Error("no buttons produced a keyboard")
	}
}

// The spinner is the user-visible clock, so it is cleared before any work.
// Without this a press looks like nothing happened for as long as the page
// takes.
func TestACallbackIsAcknowledgedBeforeAnyWork(t *testing.T) {
	api := newFakeAPI(t)

	handler := &orderRecordingHandler{api: api}
	b := newBridge(api.client(), handler, slog.New(slog.DiscardHandler))

	b.handle(context.Background(), Update{
		UpdateID: 1,
		CallbackQuery: &CallbackQuery{
			ID:   "q-1",
			Data: "p|abc|2|i",
			Message: &Message{MessageID: 5, Chat: struct {
				ID int64 `json:"id"`
			}{ID: 99}},
		},
	})

	if handler.ackedBefore != 1 {
		t.Errorf("answerCallbackQuery calls before the handler ran = %d, want 1", handler.ackedBefore)
	}
	if len(api.callsTo("sendMessage")) != 1 {
		t.Errorf("the page was not sent: %d sendMessage calls", len(api.callsTo("sendMessage")))
	}
	// The old page's button is taken away so the chat does not collect live
	// buttons pointing at stale pages.
	if len(api.callsTo("editMessageReplyMarkup")) != 1 {
		t.Error("the old keyboard was not cleared")
	}
}

type orderRecordingHandler struct {
	api         *fakeAPI
	ackedBefore int
}

func (h *orderRecordingHandler) Handle(context.Context, messaging.InboundMessage) (messaging.OutboundMessage, error) {
	return messaging.OutboundMessage{}, nil
}

func (h *orderRecordingHandler) HandleAction(context.Context, messaging.InboundAction) (messaging.OutboundMessage, error) {
	h.ackedBefore = len(h.api.callsTo("answerCallbackQuery"))
	return messaging.OutboundMessage{Text: "More places (13–24 of 30)"}, nil
}

// Tapping "Next" quickly is what paging looks like. It must be refused as a
// toast if at all, never as extra messages in the chat, and never with the
// copy written for somebody asking too many questions.
func TestRapidPressesAreRefusedQuietly(t *testing.T) {
	api := newFakeAPI(t)
	handler := &orderRecordingHandler{api: api}
	b := newBridge(api.client(), handler, slog.New(slog.DiscardHandler))

	press := Update{
		UpdateID: 1,
		CallbackQuery: &CallbackQuery{
			ID:   "q",
			Data: "p|abc|2|i",
			Message: &Message{MessageID: 5, Chat: struct {
				ID int64 `json:"id"`
			}{ID: 99}},
		},
	}
	for range actionBurst + 5 {
		b.handle(context.Background(), press)
	}

	sends := api.callsTo("sendMessage")
	if len(sends) != actionBurst {
		t.Errorf("%d pages sent for %d presses, want %d", len(sends), actionBurst+5, actionBurst)
	}
	for _, c := range sends {
		if text, _ := c.body["text"].(string); strings.Contains(text, "faster than I can think") {
			t.Error("a refused press was answered with the question limiter's copy, in the chat")
		}
	}
}

// A press that arrives while the webhook is at capacity must still be
// answered. Without this it gets silence and a button that spins until
// Telegram gives up.
func TestABusyPressIsStillAcknowledged(t *testing.T) {
	api := newFakeAPI(t)
	b := newBridge(api.client(), &orderRecordingHandler{api: api}, slog.New(slog.DiscardHandler))

	b.busy(context.Background(), Update{
		UpdateID:      1,
		CallbackQuery: &CallbackQuery{ID: "q-busy"},
	})

	acks := api.callsTo("answerCallbackQuery")
	if len(acks) != 1 {
		t.Fatalf("%d acknowledgements, want 1", len(acks))
	}
	if text, _ := acks[0].body["text"].(string); !strings.Contains(text, "Busy") {
		t.Errorf("the busy toast said %q", text)
	}
	if len(api.callsTo("sendMessage")) != 0 {
		t.Error("a busy press added a message to the chat")
	}
}
