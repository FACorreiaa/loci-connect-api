package telegram

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
)

// pollTimeout is how long Telegram holds an idle getUpdates open.
//
// Long on purpose: an idle bot then costs one request a minute instead of
// sixty, and a message still arrives the moment it is sent, because Telegram
// answers the held request rather than waiting for the next one.
const pollTimeout = 50 * time.Second

// answerTimeout bounds the work done for a single message.
//
// Generous, because planning an itinerary is slow, but bounded: without it one
// stuck generation would hold the poll loop forever and the bot would go silent
// with nothing in the logs to say why.
const answerTimeout = 3 * time.Minute

// retryDelay is how long to wait after a failed poll.
//
// Telegram being unreachable is usually brief. Retrying immediately would spin;
// backing off further would make a working bot look dead for a minute.
const retryDelay = 5 * time.Second

// Handler answers an inbound message. Satisfied by messaging.Service.
type Handler interface {
	Handle(ctx context.Context, in messaging.InboundMessage) (messaging.OutboundMessage, error)
}

// Poller receives updates for one bot and answers them.
type Poller struct {
	client  *Client
	repo    messaging.Repository
	handler Handler
	logger  *slog.Logger

	// account scopes the delivery watermark. Resolved at start, because a
	// token pointed at a different bot begins a new update sequence and must
	// not inherit the old bot's position in it.
	account Account
}

func NewPoller(client *Client, repo messaging.Repository, handler Handler, logger *slog.Logger) *Poller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{client: client, repo: repo, handler: handler, logger: logger}
}

// Run receives and answers updates until ctx is cancelled.
//
// Returns nil on cancellation: a bot told to stop has not failed.
func (p *Poller) Run(ctx context.Context) error {
	account, err := p.client.GetMe(ctx)
	if err != nil {
		return err
	}
	p.account = account
	p.logger.InfoContext(ctx, "telegram bot receiving updates",
		slog.String("bot", account.Username),
		slog.String("account_id", account.ID))

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		if err := p.pollOnce(ctx); err != nil {
			switch {
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				return nil
			case errors.Is(err, ErrConflict):
				// Two processes on one token. This never resolves by retrying,
				// and both would answer every message, so stop rather than
				// double-answer everything the other one is already handling.
				p.logger.ErrorContext(ctx, "stopping: another process is receiving this bot's updates",
					slog.String("bot", account.Username))
				return err
			}

			p.logger.WarnContext(ctx, "telegram poll failed",
				slog.String("error", err.Error()))
			if !sleep(ctx, retryDelay) {
				return nil
			}
		}
	}
}

// pollOnce reads a batch of updates and answers each one.
func (p *Poller) pollOnce(ctx context.Context) error {
	offset, err := p.repo.Cursor(ctx, messaging.PlatformTelegram, p.account.ID)
	if err != nil {
		return err
	}

	// offset is "the first update I have not seen", so it is one past the
	// watermark. Sending the watermark itself would redeliver the last message
	// on every poll.
	var from int64
	if offset > 0 {
		from = offset + 1
	}

	updates, err := p.client.GetUpdates(ctx, from, pollTimeout)
	if err != nil {
		return err
	}

	for _, update := range updates {
		p.handleUpdate(ctx, update)

		// Acknowledged after handling, not before.
		//
		// The trade is deliberate: a crash between answering and
		// acknowledging redelivers one message, and a person sees an answer
		// twice. Acknowledging first would lose the message instead, and an
		// itinerary that was silently dropped is worse than one sent twice.
		if err := p.repo.SetCursor(ctx, messaging.PlatformTelegram, p.account.ID, update.UpdateID); err != nil {
			p.logger.WarnContext(ctx, "could not advance the telegram cursor",
				slog.String("error", err.Error()))
		}
	}
	return nil
}

// handleUpdate answers one update.
//
// Failures are logged and swallowed: one message that could not be answered
// must not stop the bot receiving the next.
func (p *Poller) handleUpdate(ctx context.Context, update Update) {
	if update.Message == nil || update.Message.Text == "" {
		// Photos, stickers, joins. Nothing to answer, and the cursor still
		// advances so it is not offered again.
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
	stopTyping := p.keepTyping(answerCtx, chatID)
	out, err := p.handler.Handle(answerCtx, in)
	stopTyping()

	if err != nil {
		p.logger.ErrorContext(ctx, "could not handle a telegram message",
			slog.String("error", err.Error()))
		// The reply is generic on purpose: the error can name internal paths.
		out = messaging.OutboundMessage{Text: "Something went wrong. Try again in a moment."}
	}
	if out.Silent || out.Text == "" {
		return
	}

	// Sent on ctx rather than answerCtx: the answer is ready, and letting the
	// generation's deadline cancel its own delivery would waste the work.
	if err := p.client.SendMessage(ctx, chatID, out.Text); err != nil {
		p.logger.ErrorContext(ctx, "could not send a telegram reply",
			slog.String("error", err.Error()))
	}
}

// keepTyping shows the indicator until the returned function is called.
func (p *Poller) keepTyping(ctx context.Context, chatID string) func() {
	typingCtx, cancel := context.WithCancel(ctx)

	go func() {
		// Telegram clears the indicator after about five seconds.
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		p.client.SendTyping(typingCtx, chatID)
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				p.client.SendTyping(typingCtx, chatID)
			}
		}
	}()

	return cancel
}

// sleep waits, reporting false if the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
