package telegram

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
	"github.com/FACorreiaa/loci-connect-api/pkg/observability"
)

// timed records how long one stage of answering a recording took.
//
// The deadlines in this file were chosen before there was any traffic to
// choose them from; this is what says which of them are wrong.
func timed[T any](stage string, fn func() (T, error)) (T, error) {
	start := time.Now()
	result, err := fn()
	observability.VoiceStageSeconds.WithLabelValues(stage).Observe(time.Since(start).Seconds())
	return result, err
}

// The budget for one update.
//
// One deadline for the whole thing used to be enough, when the whole thing was
// a generation. A recording adds a fetch, a transcription and — after the
// answer has already been sent — a synthesis and an upload, and a single
// deadline over all of it either strangles the generation or lets a stuck
// fetch hold everything. So each leg bounds itself, and updateTimeout is the
// backstop: without it a wedged leg holds one of four in-flight slots forever,
// which is a quarter of the bot.
const (
	// updateTimeout is the ceiling for everything one update can cost, and it
	// has to exceed the legs below added together — otherwise the worst case is
	// a deadline firing after seven minutes of work and before the reply is
	// sent, which is the one outcome worse than being slow. See the test that
	// pins this.
	updateTimeout = 10 * time.Minute

	getFileTimeout  = 10 * time.Second
	downloadTimeout = 30 * time.Second

	// transcribeTimeout is generous because the transcription runs on CPU.
	//
	// Measured on the cluster's own service: roughly two and a half to three
	// times the length of the clip, on a single replica shared with other
	// apps, so a thirty-second recording is over a minute of work and a queue
	// behind somebody else's is ordinary. Sixty seconds — the obvious number —
	// would have timed out most real recordings.
	transcribeTimeout = 4 * time.Minute

	// answerTimeout bounds working out the answer itself.
	//
	// Unchanged, and deliberately: planning an itinerary is slow, three
	// minutes is what that was measured against, and nothing about a typed
	// message should behave differently now.
	answerTimeout = 3 * time.Minute

	sendTimeout = 45 * time.Second
)

// chatBurst is how many answers one chat may ask for at once.
//
// A person asks a follow-up every few seconds at most and then waits for a
// plan; three in hand and one every ten seconds is well clear of that and well
// under what a script can do.
const chatBurst = 3

// chatEvery is how often one chat may be answered. A var rather than a const
// because rate.Every is a function call.
var chatEvery = rate.Every(10 * time.Second)

// actionBurst and actionEvery ration button presses, separately from questions
// and much faster.
//
// Sharing the answer limiter would refuse an ordinary "Next, Next, Next" —
// three in ten seconds is exactly what paging looks like — and would refuse it
// with copy written for somebody asking too many questions. The justification
// is pricing: a page is one database read and one sendMessage, on the order of
// a thousandth of what a generation costs, so metering it at the generation's
// rate is mispriced rather than careful.
const actionBurst = 10

var actionEvery = rate.Every(2 * time.Second)

// pageTimeout bounds rendering one page. A page is a read of something already
// written, so the three minutes an itinerary may take does not apply.
const pageTimeout = 20 * time.Second

// maxEchoChars caps the transcript echoed back before the answer.
//
// The echo is a check on what was heard, not a transcript service, and a
// rambling minute should not fill the screen before the plan arrives.
const maxEchoChars = 300

// Handler answers an inbound message. Satisfied by messaging.Service.
type Handler interface {
	Handle(ctx context.Context, in messaging.InboundMessage) (messaging.OutboundMessage, error)
	// HandleAction answers a button press. Separate from Handle because a
	// press is not a question: it names something already produced.
	HandleAction(ctx context.Context, in messaging.InboundAction) (messaging.OutboundMessage, error)
}

// Voice is the part of the speech client this adapter uses.
//
// An interface here rather than a dependency on the speech package, so the
// adapter states what it needs and its tests do not need a provider. Nil means
// the deployment has no speech configured, and recordings are refused politely
// instead of being ignored.
type Voice interface {
	// hint is vocabulary to bias decoding toward. Without it the service turns
	// "Cais do Sodré" into "Case 2 Soda" and an itinerary is planned for
	// somewhere that does not exist.
	Transcribe(ctx context.Context, audio []byte, mimeType, hint string) (string, error)
	Enabled() bool
}

// Vocabulary supplies the place names a speaker is likely to use.
//
// Looked up against the account the chat belongs to, which is why it is
// consulted inside the closure rather than when the update arrives: before the
// link is resolved there is nobody to look anything up for.
type Vocabulary interface {
	For(ctx context.Context, userID uuid.UUID) string
}

// VoiceOptions are the limits a recording is held to.
type VoiceOptions struct {
	MaxDuration       time.Duration
	MaxVideoDuration  time.Duration
	MaxBytes          int64
	VideoNotesEnabled bool
}

// bridge turns one Telegram update into a reply.
//
// Shared by the poller and the webhook so the two delivery modes answer a
// message identically — same timeouts, same typing indicator, same generic
// error reply. How an update arrives is the only thing that differs.
type bridge struct {
	client  *Client
	handler Handler
	logger  *slog.Logger

	voice      Voice
	voiceOpts  VoiceOptions
	vocabulary Vocabulary

	// perChat rations what one conversation can ask for. The daily quota is a
	// counter and says nothing about a burst; this is what keeps one sender
	// from spending everybody else's capacity in ten seconds.
	perChat *chatLimiter
	// perChatActions rations button presses. See actionBurst.
	perChatActions *chatLimiter
}

func newBridge(client *Client, handler Handler, logger *slog.Logger) bridge {
	return bridge{
		client:         client,
		handler:        handler,
		logger:         logger,
		perChat:        newChatLimiter(chatEvery, chatBurst),
		perChatActions: newChatLimiter(actionEvery, actionBurst),
	}
}

// withVoice returns a bridge that can hear recordings.
func (b bridge) withVoice(voice Voice, vocabulary Vocabulary, opts VoiceOptions) bridge {
	if voice == nil || !voice.Enabled() {
		return b
	}
	b.voice = voice
	b.vocabulary = vocabulary
	b.voiceOpts = opts
	return b
}

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

	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	if update.CallbackQuery != nil {
		b.answerCallback(ctx, update.CallbackQuery)
		return
	}
	b.answer(ctx, update)
}

// answerCallback renders the page a button asked for.
//
// The first thing it does is clear the spinner. Telegram keeps a pressed button
// spinning for several seconds until answerCallbackQuery arrives, so that call
// is the user-visible clock and it goes before any work — its own failure is
// never a reason to withhold the page.
//
// A page is sent as a new message rather than edited into the old one. An
// edited message is subject to the same 4096-character cap and cannot be
// split, so paging in place would have a hard length ceiling that sending does
// not. The old message's button is removed instead, so a chat does not collect
// live buttons all pointing at the same stale page.
func (b bridge) answerCallback(ctx context.Context, q *CallbackQuery) {
	b.client.AnswerCallbackQuery(ctx, q.ID, "")

	if q.Message == nil {
		return
	}
	chatID := chatIDOf(q.Message.Chat.ID)

	if !b.perChatActions.allow(chatID) {
		// A toast, not a message: refusing a tap should not add to the chat.
		b.client.AnswerCallbackQuery(ctx, q.ID, "One moment…")
		b.logger.InfoContext(ctx, "telegram chat is paging faster than it is served",
			slog.String("chat_id", chatID))
		return
	}

	pageCtx, cancel := context.WithTimeout(ctx, pageTimeout)
	out, err := b.handler.HandleAction(pageCtx, messaging.InboundAction{
		Platform: messaging.PlatformTelegram,
		ChatID:   chatID,
		Data:     q.Data,
	})
	cancel()

	if err != nil {
		b.logger.ErrorContext(ctx, "could not handle a telegram button press",
			slog.String("error", err.Error()))
		out = messaging.OutboundMessage{Text: "Something went wrong. Try again in a moment."}
	}
	if out.Silent || out.Text == "" {
		return
	}

	sendCtx, cancelSend := context.WithTimeout(ctx, sendTimeout)
	defer cancelSend()

	// Strip the button off the page that was just read. Best effort: Telegram
	// errors when the markup is already what it is being set to, which is the
	// harmless case of a double tap.
	if err := b.client.EditMessageReplyMarkup(sendCtx, chatID, q.Message.MessageID, nil); err != nil {
		b.logger.DebugContext(ctx, "could not clear a telegram keyboard",
			slog.String("error", err.Error()))
	}
	b.sendWithButtons(sendCtx, chatID, out)
}

// sendWithButtons delivers a reply and whatever it offers next.
func (b bridge) sendWithButtons(ctx context.Context, chatID string, out messaging.OutboundMessage) {
	if err := b.client.SendMessageWithMarkup(ctx, chatID, out.Text, keyboardFor(b.logger, out.Buttons)); err != nil {
		b.logger.ErrorContext(ctx, "could not send a telegram reply",
			slog.String("error", err.Error()))
	}
}

// keyboardFor turns platform-neutral buttons into Telegram's shape, one per
// row, dropping any whose token Telegram would reject.
//
// Dropped rather than truncated: a token cut to 64 bytes still sends, and then
// decodes to a different page or to nothing at all. Losing the button is
// recoverable — the text says what it was for — and a button that silently
// does the wrong thing is not.
func keyboardFor(logger *slog.Logger, buttons []messaging.Button) *InlineKeyboard {
	if len(buttons) == 0 {
		return nil
	}
	rows := make([][]InlineButton, 0, len(buttons))
	for _, button := range buttons {
		if len(button.Data) > maxCallbackDataBytes {
			logger.Warn("dropping a telegram button whose token is too long",
				slog.Int("bytes", len(button.Data)))
			continue
		}
		rows = append(rows, []InlineButton{{Text: button.Label, CallbackData: button.Data}})
	}
	if len(rows) == 0 {
		return nil
	}
	return &InlineKeyboard{InlineKeyboard: rows}
}

// busy tells a chat that the bot is at capacity.
//
// Separate from answer because nothing about it should be able to take time:
// it is the reply sent when there is no capacity to work out a real one.
func (b bridge) busy(ctx context.Context, update Update) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	// A press at capacity has to be answered too. Without this it gets total
	// silence and a button that spins until Telegram gives up — the one
	// failure mode worse than a refusal.
	if q := update.CallbackQuery; q != nil {
		b.client.AnswerCallbackQuery(ctx, q.ID, "Busy — try that again in a moment.")
		return
	}
	if update.Message == nil {
		return
	}

	b.send(ctx, chatIDOf(update.Message.Chat.ID),
		"I have my hands full at the moment. Send that again in a minute.")
}

func (b bridge) answer(ctx context.Context, update Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	chatID := chatIDOf(msg.Chat.ID)

	// A caption is where Telegram puts a note sent with a recording; there is
	// nothing in Text for those.
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	var clip *Recording
	if text == "" {
		var refusal string
		clip, refusal = b.clipOf(msg)
		switch {
		case refusal != "":
			b.send(ctx, chatID, refusal)
			return
		case clip == nil:
			// Photos, stickers, joins. Nothing to answer.
			return
		}
	}

	if !b.perChat.allow(chatID) {
		b.logger.InfoContext(ctx, "telegram chat is asking faster than it is answered",
			slog.String("chat_id", chatID))
		b.send(ctx, chatID, "That is faster than I can think. Give me a moment and ask again.")
		return
	}

	in := messaging.InboundMessage{
		Platform: messaging.PlatformTelegram,
		ChatID:   chatID,
		Text:     text,
	}
	if from := msg.From; from != nil {
		in.DisplayName = displayNameOf(from.FirstName, from.Username)
	}
	if clip != nil {
		// Passed as a function, not as bytes: fetching and transcribing costs
		// the cluster real CPU on a service shared with other apps, and the
		// messaging service is the only thing that knows whether this chat
		// belongs to an account that has paid for it.
		in.Audio = func(ctx context.Context, userID uuid.UUID) (string, error) {
			return b.hear(ctx, chatID, userID, msg, clip)
		}
	}

	answerCtx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()

	// The indicator is refreshed while the answer is being worked out, because
	// Telegram clears it after a few seconds and an itinerary takes longer than
	// that — without this the chat looks idle for most of the wait.
	stopTyping := b.keepAction(answerCtx, chatID, "typing")
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
	sendCtx, cancelSend := context.WithTimeout(ctx, sendTimeout)
	b.sendWithButtons(sendCtx, chatID, out)
	cancelSend()
}

// clipOf picks the recording out of a message and holds it to its limits.
//
// It returns a refusal rather than an error when the recording is one this
// deployment will not take, because the sender is waiting and "nothing
// happened" is the worst of the possible answers. Everything is decided from
// the update itself: refusing a two-minute recording here costs nothing, and
// refusing it after the download costs a download and a transcription for an
// answer nobody gets.
func (b bridge) clipOf(msg *Message) (*Recording, string) {
	switch {
	case msg.Voice != nil:
		if b.voice == nil {
			return nil, "I cannot listen to recordings right now — type it instead."
		}
		return b.withinLimits(msg.Voice, b.voiceOpts.MaxDuration, "voice note", "voice")

	case msg.VideoNote != nil:
		if b.voice == nil {
			return nil, "I cannot listen to recordings right now — type it instead."
		}
		if !b.voiceOpts.VideoNotesEnabled {
			observability.VoiceMessagesTotal.WithLabelValues("video_note", "disabled").Inc()
			return nil, "I cannot take video messages. Send it as a voice note and I will listen."
		}
		return b.withinLimits(msg.VideoNote, b.voiceOpts.MaxVideoDuration, "video message", "video_note")

	default:
		return nil, ""
	}
}

func (b bridge) withinLimits(clip *Recording, maxDuration time.Duration, kind, metric string) (*Recording, string) {
	if maxDuration > 0 && time.Duration(clip.Duration)*time.Second > maxDuration {
		observability.VoiceMessagesTotal.WithLabelValues(metric, "too_long").Inc()
		return nil, "That is a long " + kind + ". Keep it under " +
			plainDuration(maxDuration) + " and I will catch all of it."
	}
	if b.voiceOpts.MaxBytes > 0 && clip.FileSize > b.voiceOpts.MaxBytes {
		observability.VoiceMessagesTotal.WithLabelValues(metric, "too_big").Inc()
		return nil, "That " + kind + " is too big for me to fetch. A shorter one will work."
	}
	return clip, ""
}

// hear fetches a recording and returns what was said.
//
// The transcript is echoed back before the answer is worked out. Two reasons:
// speech recognition mangles place names, and somebody who can see "Alfama"
// came through as "alarm" knows to say it again rather than waiting out a
// wrong itinerary. And an itinerary takes minutes, so it is also the only
// early sign that the recording arrived at all.
func (b bridge) hear(ctx context.Context, chatID string, userID uuid.UUID, msg *Message, clip *Recording) (string, error) {
	kind := kindOf(msg)

	file, err := timed("get_file", func() (File, error) {
		fileCtx, cancel := context.WithTimeout(ctx, getFileTimeout)
		defer cancel()
		return b.client.GetFile(fileCtx, clip.FileID)
	})
	if err != nil {
		observability.VoiceMessagesTotal.WithLabelValues(kind, "failed").Inc()
		return "", err
	}

	limit := b.voiceOpts.MaxBytes
	if limit <= 0 {
		limit = defaultMaxBytes
	}
	audio, err := timed("download", func() ([]byte, error) {
		downloadCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
		defer cancel()
		return b.client.Download(downloadCtx, file.FilePath, limit)
	})
	if err != nil {
		observability.VoiceMessagesTotal.WithLabelValues(kind, "failed").Inc()
		return "", err
	}

	// The hint is looked up now rather than when the update arrived, because
	// it is the account's places and until the link was resolved there was no
	// account to look them up for.
	var hint string
	if b.vocabulary != nil {
		hint = b.vocabulary.For(ctx, userID)
	}

	transcript, err := timed("transcribe", func() (string, error) {
		transcribeCtx, cancel := context.WithTimeout(ctx, transcribeTimeout)
		defer cancel()
		return b.voice.Transcribe(transcribeCtx, audio, mimeTypeOf(clip), hint)
	})
	if err != nil {
		observability.VoiceMessagesTotal.WithLabelValues(kind, "failed").Inc()
		return "", err
	}
	if strings.TrimSpace(transcript) == "" {
		observability.VoiceMessagesTotal.WithLabelValues(kind, "no_speech").Inc()
		return "", nil
	}
	observability.VoiceMessagesTotal.WithLabelValues(kind, "transcribed").Inc()

	b.send(ctx, chatID, `Heard: "`+truncate(transcript, maxEchoChars)+`"`)
	return transcript, nil
}

// kindOf names the sort of recording a message carries, for metrics.
func kindOf(msg *Message) string {
	if msg != nil && msg.VideoNote != nil {
		return "video_note"
	}
	return "voice"
}

// send delivers one message, logging rather than returning a failure.
func (b bridge) send(ctx context.Context, chatID, text string) {
	if err := b.client.SendMessage(ctx, chatID, text); err != nil {
		b.logger.ErrorContext(ctx, "could not send a telegram message",
			slog.String("error", err.Error()))
	}
}

// keepAction shows an activity indicator until the returned function is called.
func (b bridge) keepAction(ctx context.Context, chatID, action string) func() {
	actionCtx, cancel := context.WithCancel(ctx)

	go func() {
		// An indicator is decoration; a panic in it must not be fatal.
		defer func() {
			if r := recover(); r != nil {
				b.logger.ErrorContext(ctx, "panic while sending a telegram activity indicator",
					slog.Any("panic", r))
			}
		}()
		// Telegram clears the indicator after about five seconds.
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		b.client.SendAction(actionCtx, chatID, action)
		for {
			select {
			case <-actionCtx.Done():
				return
			case <-ticker.C:
				b.client.SendAction(actionCtx, chatID, action)
			}
		}
	}()

	return cancel
}

// defaultMaxBytes is what a download is held to when nothing says otherwise.
// Telegram refuses getFile past twenty megabytes, so this matches it.
const defaultMaxBytes int64 = 20 << 20

// mimeTypeOf is what a recording should be described to the model as.
//
// Telegram gives a voice note's type but not a video message's, which is
// always MP4.
func mimeTypeOf(clip *Recording) string {
	if clip.MIMEType != "" {
		return clip.MIMEType
	}
	return "video/mp4"
}

// plainDuration renders a limit the way somebody would say it, because this
// ends up in a sentence rather than in a log line.
func plainDuration(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		if minutes := int(d / time.Minute); minutes == 1 {
			return "a minute"
		} else {
			return strconv.Itoa(minutes) + " minutes"
		}
	}
	return strconv.Itoa(int(d/time.Second)) + " seconds"
}
