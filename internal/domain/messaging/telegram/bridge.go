package telegram

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

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
	updateTimeout = 6 * time.Minute

	getFileTimeout    = 10 * time.Second
	downloadTimeout   = 30 * time.Second
	transcribeTimeout = 60 * time.Second

	// answerTimeout bounds working out the answer itself.
	//
	// Unchanged, and deliberately: planning an itinerary is slow, three
	// minutes is what that was measured against, and nothing about a typed
	// message should behave differently now.
	answerTimeout = 3 * time.Minute

	shortenTimeout    = 20 * time.Second
	synthesiseTimeout = 45 * time.Second
	encodeTimeout     = 15 * time.Second
	sendTimeout       = 45 * time.Second
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

// maxEchoChars caps the transcript echoed back before the answer.
//
// The echo is a check on what was heard, not a transcript service, and a
// rambling minute should not fill the screen before the plan arrives.
const maxEchoChars = 300

// Handler answers an inbound message. Satisfied by messaging.Service.
type Handler interface {
	Handle(ctx context.Context, in messaging.InboundMessage) (messaging.OutboundMessage, error)
}

// Voice is the part of the speech client this adapter uses.
//
// An interface here rather than a dependency on the speech package, so the
// adapter states what it needs and its tests do not need a provider. Nil means
// the deployment has no speech configured, and recordings are refused politely
// instead of being ignored.
type Voice interface {
	Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error)
	Shorten(ctx context.Context, text string) (string, error)
	Say(ctx context.Context, text string, encodeTimeout time.Duration) ([]byte, error)
	CanSpeak() bool
}

// VoiceOptions are the limits a recording is held to.
type VoiceOptions struct {
	MaxDuration       time.Duration
	MaxVideoDuration  time.Duration
	MaxBytes          int64
	RepliesEnabled    bool
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

	voice     Voice
	voiceOpts VoiceOptions

	// perChat rations what one conversation can ask for. The daily quota is a
	// counter and says nothing about a burst; this is what keeps one sender
	// from spending everybody else's capacity in ten seconds.
	perChat *chatLimiter
}

func newBridge(client *Client, handler Handler, logger *slog.Logger) bridge {
	return bridge{
		client:  client,
		handler: handler,
		logger:  logger,
		perChat: newChatLimiter(chatEvery, chatBurst),
	}
}

// withVoice returns a bridge that can hear recordings and say replies.
func (b bridge) withVoice(voice Voice, opts VoiceOptions) bridge {
	if voice == nil {
		return b
	}
	b.voice = voice
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
	b.answer(ctx, update)
}

// busy tells a chat that the bot is at capacity.
//
// Separate from answer because nothing about it should be able to take time:
// it is the reply sent when there is no capacity to work out a real one.
func (b bridge) busy(ctx context.Context, update Update) {
	if update.Message == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

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
		// money, and the service is the only thing that knows whether this
		// chat belongs to an account that has paid for it.
		in.Audio = func(ctx context.Context) (string, error) { return b.hear(ctx, chatID, msg, clip) }
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
	if err := b.client.SendMessage(sendCtx, chatID, out.Text); err != nil {
		b.logger.ErrorContext(ctx, "could not send a telegram reply",
			slog.String("error", err.Error()))
	}
	cancelSend()

	// Only after the written answer has landed. Everything below is additive:
	// if any of it fails the person already has their plan, so it is logged
	// and swallowed rather than turned into an apology.
	if clip != nil {
		b.speak(ctx, chatID, out)
	}
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
func (b bridge) hear(ctx context.Context, chatID string, msg *Message, clip *Recording) (string, error) {
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

	transcript, err := timed("transcribe", func() (string, error) {
		transcribeCtx, cancel := context.WithTimeout(ctx, transcribeTimeout)
		defer cancel()
		return b.voice.Transcribe(transcribeCtx, audio, mimeTypeOf(clip))
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

// speak sends the answer as a voice note, beside the written one.
//
// Every failure here is logged and swallowed. The written answer has already
// arrived, and a synthesis that did not work is not a reason to tell somebody
// their itinerary failed.
func (b bridge) speak(ctx context.Context, chatID string, out messaging.OutboundMessage) {
	if !b.voiceOpts.RepliesEnabled || b.voice == nil || !b.voice.CanSpeak() {
		observability.VoiceRepliesTotal.WithLabelValues("skipped").Inc()
		return
	}

	spoken := out.Speak
	if spoken == "" {
		// The service writes a spoken form for its own replies. This one is a
		// generated answer, which is a plan full of addresses and links —
		// unusable read aloud, so it is reduced to what is worth hearing.
		short, err := timed("shorten", func() (string, error) {
			shortenCtx, cancel := context.WithTimeout(ctx, shortenTimeout)
			defer cancel()
			return b.voice.Shorten(shortenCtx, out.Text)
		})
		if err != nil {
			observability.VoiceRepliesTotal.WithLabelValues("failed").Inc()
			b.logger.WarnContext(ctx, "could not shorten a reply for speaking",
				slog.String("error", err.Error()))
			return
		}
		spoken = short
	}

	// The indicator is the only thing saying this extra wait is deliberate
	// rather than the bot having stopped.
	stopRecording := b.keepAction(ctx, chatID, "record_voice")
	defer stopRecording()

	ogg, err := timed("synthesise", func() ([]byte, error) {
		sayCtx, cancel := context.WithTimeout(ctx, synthesiseTimeout)
		defer cancel()
		return b.voice.Say(sayCtx, spoken, encodeTimeout)
	})
	if err != nil {
		observability.VoiceRepliesTotal.WithLabelValues("failed").Inc()
		b.logger.WarnContext(ctx, "could not say a reply aloud",
			slog.String("error", err.Error()))
		return
	}
	stopRecording()

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if _, err := timed("send_voice", func() (struct{}, error) {
		return struct{}{}, b.client.SendVoice(sendCtx, chatID, ogg, 0, "")
	}); err != nil {
		observability.VoiceRepliesTotal.WithLabelValues("failed").Inc()
		b.logger.WarnContext(ctx, "could not send a spoken reply",
			slog.String("error", err.Error()))
		return
	}
	observability.VoiceRepliesTotal.WithLabelValues("spoken").Inc()
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
