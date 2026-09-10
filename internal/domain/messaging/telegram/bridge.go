package telegram

import (
	"context"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
)

// answerTimeout bounds the work done for a single message.
//
// Generous, because planning an itinerary is slow, but bounded: without it one
// stuck generation would hold the poll loop forever and the bot would go silent
// with nothing in the logs to say why.
const answerTimeout = 3 * time.Minute

// Handler answers an inbound message. Satisfied by messaging.Service.
type Handler interface {
	Handle(ctx context.Context, in messaging.InboundMessage) (messaging.OutboundMessage, error)
}

// bridge turns one Telegram update into a reply.
//
// Shared by the poller and the webhook so the two delivery modes answer a
// message identically — same timeout, same typing indicator, same generic
// error reply. How an update arrives is the only thing that differs.
type bridge struct {
	client  *Client
	handler Handler
	logger  *slog.Logger
}

// handle answers one update.
//
// Failures are logged and swallowed: one message that could not be answered
// must not stop the bot receiving the next.
// handle answers one update. It never lets a panic escape: the webhook runs
// it on a detached goroutine, where an unrecovered panic is not a failed
// request but the whole API process gone — one malformed message from one
// chat must not take api.lociai.fyi down for everybody.
func (b bridge) handle(ctx context.Context, update Update) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.ErrorContext(ctx, "panic while handling a telegram update",
				slog.Any("panic", r), slog.Int64("update_id", update.UpdateID))
		}
	}()
	b.answer(ctx, update)
}

func (b bridge) answer(ctx context.Context, update Update) {
	if update.Message == nil || update.Message.Text == "" {
		// Photos, stickers, joins. Nothing to answer.
		return
	}

	chatID := chatIDOf(update.Message.Chat.ID)
	in := messaging.InboundMessage{
		Platform: messaging.PlatformTelegram,
		ChatID:   chatID,
		Text:     update.Message.Text,
	}
	if from := update.Message.From; from != nil {
		in.DisplayName = displayNameOf(from.FirstName, from.Username)
	}

	answerCtx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()

	// The indicator is refreshed while the answer is being worked out, because
	// Telegram clears it after a few seconds and an itinerary takes longer than
	// that — without this the chat looks idle for most of the wait.
	stopTyping := b.keepTyping(answerCtx, chatID)
	out, err := b.handler.Handle(answerCtx, in)
	stopTyping()

	if err != nil {
		b.logger.ErrorContext(ctx, "could not handle a telegram message",
			slog.String("error", err.Error()))
		// The reply is generic on purpose: the error can name internal paths.
		out = messaging.OutboundMessage{Text: "Something went wrong. Try again in a moment."}
	}
	if out.Silent || out.Text == "" {
		return
	}

	// Sent on ctx rather than answerCtx: the answer is ready, and letting the
	// generation's deadline cancel its own delivery would waste the work.
	if err := b.client.SendMessage(ctx, chatID, out.Text); err != nil {
		b.logger.ErrorContext(ctx, "could not send a telegram reply",
			slog.String("error", err.Error()))
	}
}

// keepTyping shows the indicator until the returned function is called.
func (b bridge) keepTyping(ctx context.Context, chatID string) func() {
	typingCtx, cancel := context.WithCancel(ctx)

	go func() {
		// A typing indicator is decoration; a panic in it must not be fatal.
		defer func() {
			if r := recover(); r != nil {
				b.logger.ErrorContext(ctx, "panic while sending a telegram typing indicator", slog.Any("panic", r))
			}
		}()
		// Telegram clears the indicator after about five seconds.
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		b.client.SendTyping(typingCtx, chatID)
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				b.client.SendTyping(typingCtx, chatID)
			}
		}
	}()

	return cancel
}
