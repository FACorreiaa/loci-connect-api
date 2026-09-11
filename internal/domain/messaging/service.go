package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/subscription"
)

// PlatformTelegram is the only platform today. Named rather than spelled as a
// literal in five places.
const PlatformTelegram = "telegram"

// codeTTL is how long a link code lives.
//
// Long enough to read it off one screen and type it into another, short enough
// that a code left in a chat log is worthless. This is one of the two things
// making a hand-typed code safe; the other is that it works once.
const codeTTL = 15 * time.Minute

// InboundMessage is one message from a chat platform.
type InboundMessage struct {
	Platform string
	// ChatID identifies the conversation on the platform.
	ChatID string
	// DisplayName is how the sender is addressed there, for the settings page.
	DisplayName string
	Text        string

	// Audio is a recording waiting to be understood, or nil for an ordinary
	// message.
	//
	// A function rather than the bytes, and rather than a transcript already
	// taken, because fetching and transcribing one costs money and anybody can
	// message a bot. Taking the transcript before the chat is known would let
	// a stranger with no account spend the owner's credits a minute of audio
	// at a time. So this is called only once the chat has been resolved to an
	// account and that account's quota has covered it.
	//
	// It returns an empty transcript with no error when there was no speech in
	// the recording, which is an ordinary outcome — a pocket, or a minute of
	// traffic noise — rather than a failure.
	Audio func(context.Context) (string, error)
}

// SpokenBy reports whether this message was spoken rather than typed.
func (m InboundMessage) SpokenBy() bool { return m.Audio != nil }

// OutboundMessage is the reply.
type OutboundMessage struct {
	Text string
	// Silent suppresses the reply entirely. Used where answering would be
	// noise — a message this deployment has already handled.
	Silent bool

	// Speak is what to say aloud, for a platform that can and a sender who
	// used their voice.
	//
	// Empty means "work it out from Text", which costs a model call. The
	// service fills this in for the replies it writes itself — they are
	// already a sentence or two, and shortening a sentence into a sentence is
	// a call spent on nothing — and leaves it empty only for an answer it did
	// not write.
	Speak string
}

// Answerer turns a question from a linked account into an answer.
//
// An interface so this package knows nothing about itineraries, and so the
// implementation can be the same conversation the web app uses: a question
// asked in a chat continues in the web chat and back, because both go through
// one session rather than two histories that happen to belong to one person.
type Answerer interface {
	Answer(ctx context.Context, userID uuid.UUID, email, text string) (string, error)
}

// Quota meters what a chat costs against the account it is linked to.
//
// An interface so this package does not depend on the subscription domain, and
// so it states the one thing it needs. Satisfied by subscription.Service.
type Quota interface {
	ConsumeQuota(ctx context.Context, userID uuid.UUID, email string) error
}

// Service links chats to accounts and routes what they send.
type Service struct {
	repo      Repository
	answerer  Answerer
	quota     Quota
	botHandle string
	logger    *slog.Logger
	now       func() time.Time
}

func NewService(repo Repository, answerer Answerer, botHandle string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:      repo,
		answerer:  answerer,
		botHandle: strings.TrimSpace(botHandle),
		logger:    logger,
		now:       time.Now,
	}
}

// WithClock replaces the service's clock, for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// WithQuota meters answered messages against the account's daily plan.
//
// A builder rather than a constructor argument so the existing callers and
// their tests are untouched, and so a nil quota keeps meaning "unmetered",
// which is what a test that is not about metering wants.
func (s *Service) WithQuota(q Quota) *Service {
	s.quota = q
	return s
}

// BotHandle is where codes are sent. Empty when no bot is configured, which
// the settings page shows instead of issuing a code nothing can redeem.
func (s *Service) BotHandle() string { return s.botHandle }

// Status returns a user's link on a platform.
func (s *Service) Status(ctx context.Context, userID uuid.UUID, platform string) (Link, error) {
	return s.repo.LinkForUser(ctx, userID, platform)
}

// IssueCode mints a code for an authenticated user to send to the bot.
//
// The code is returned once and only its hash is stored.
func (s *Service) IssueCode(ctx context.Context, userID uuid.UUID, platform string) (string, time.Time, error) {
	if platform != PlatformTelegram {
		return "", time.Time{}, fmt.Errorf("messaging: %q is not a platform Loci links", platform)
	}

	code, err := NewCode()
	if err != nil {
		return "", time.Time{}, errors.New("messaging: could not generate a link code")
	}

	expiresAt := s.now().Add(codeTTL)
	if err := s.repo.CreateCode(ctx, userID, platform, HashCode(code), expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return code, expiresAt, nil
}

// Unlink disconnects a user's chat. The conversation history stays with the
// account; only the route to it is removed.
func (s *Service) Unlink(ctx context.Context, userID uuid.UUID, platform string) error {
	return s.repo.Unlink(ctx, userID, platform)
}

// Handle answers one inbound message.
//
// The order is deliberate. Commands are recognised before anything else, so
// they never reach the model and never spend the owner's quota — "/help" must
// not cost a generation. An unlinked chat can reach exactly one thing, a code,
// and is otherwise told how to link.
func (s *Service) Handle(ctx context.Context, in InboundMessage) (OutboundMessage, error) {
	text := strings.TrimSpace(in.Text)

	link, err := s.repo.LinkForChat(ctx, in.Platform, in.ChatID)
	switch {
	case errors.Is(err, ErrNotLinked):
		return s.handleUnlinked(ctx, in, text)
	case err != nil:
		return OutboundMessage{}, err
	}

	// Only recognised commands are intercepted. "/lisbon in march" is a
	// question with a slash in front of it, and answering it is friendlier
	// than refusing it over punctuation.
	//
	// A recording cannot be one: nobody says "slash help", so a transcript
	// never carries the slash this matches on. The transcript is also what
	// would tell us it said "help", and taking it is the expense — there is no
	// order of operations that makes a spoken command free.
	if !in.SpokenBy() {
		if cmd, arg := parseCommand(text); cmd != cmdNone && cmd != cmdUnknown {
			return s.handleCommand(ctx, in, link, cmd, arg)
		}

		if text == "" {
			return OutboundMessage{
				Text:  "Send me a place and I will plan something. Try \"three days in Lisbon\".",
				Speak: "Send me a place and I will plan something.",
			}, nil
		}
	}

	if err := s.repo.TouchLink(ctx, in.Platform, in.ChatID, in.DisplayName); err != nil {
		// Not fatal: failing to record that a chat was used is no reason to
		// refuse to answer it.
		s.logger.WarnContext(ctx, "could not record chat activity", slog.String("error", err.Error()))
	}

	if out, spent := s.spendQuota(ctx, link); !spent {
		return out, nil
	}

	if s.answerer == nil {
		return OutboundMessage{
			Text:  "I cannot answer right now. Try again shortly.",
			Speak: "I cannot answer right now. Try again shortly.",
		}, nil
	}

	// Only now, with the chat resolved and the request paid for, is the
	// recording worth fetching.
	if in.SpokenBy() {
		spoken, err := in.Audio(ctx)
		switch {
		case err != nil:
			s.logger.ErrorContext(ctx, "could not understand a recording",
				slog.String("user_id", link.UserID.String()),
				slog.String("error", err.Error()))
			return OutboundMessage{
				Text:  "I could not make that out. Try again, or type it.",
				Speak: "I could not make that out. Try again, or type it.",
			}, nil
		case strings.TrimSpace(spoken) == "":
			return OutboundMessage{
				Text:  "I could not hear anything in that.",
				Speak: "I could not hear anything in that.",
			}, nil
		}
		text = strings.TrimSpace(spoken)
	}

	answer, err := s.answerer.Answer(ctx, link.UserID, link.Email, text)
	if err != nil {
		s.logger.ErrorContext(ctx, "could not answer a chat message",
			slog.String("user_id", link.UserID.String()),
			slog.String("error", err.Error()))
		// The upstream error is not repeated to the chat: it can name models,
		// providers and internal paths, none of which the sender can act on.
		return OutboundMessage{
			Text:  "Something went wrong working that out. Try again in a moment.",
			Speak: "Something went wrong working that out. Try again in a moment.",
		}, nil
	}
	return OutboundMessage{Text: answer}, nil
}

// spendQuota charges one request to the account behind a chat.
//
// It runs after the commands, not before them: Handle's ordering already says
// that a recognised command never reaches the model, and "/help" must not cost
// somebody a generation to read instructions.
//
// Until now nothing metered this path at all — the webhook is mounted outside
// the Connect interceptor chain, so a message sent to the bot was a way around
// the plan limit the same message would have hit in the app. Answering the
// same question should cost the same wherever it was asked.
func (s *Service) spendQuota(ctx context.Context, link Link) (OutboundMessage, bool) {
	if s.quota == nil {
		return OutboundMessage{}, true
	}

	err := s.quota.ConsumeQuota(ctx, link.UserID, link.Email)
	switch {
	case err == nil:
		return OutboundMessage{}, true

	case errors.Is(err, subscription.ErrQuotaExceeded):
		return OutboundMessage{
			Text: "That is today's requests used up on your plan. They reset at midnight UTC — " +
				"or open Loci under Settings › Plan to lift the limit.",
			Speak: "That is today's requests used up on your plan. They reset at midnight.",
		}, false

	default:
		// The counter itself failed, which is not the sender's doing. Answering
		// anyway would make a database blip a free pass, so it refuses, but it
		// refuses in a way that says to try again rather than to upgrade.
		s.logger.ErrorContext(ctx, "could not meter a chat message",
			slog.String("user_id", link.UserID.String()),
			slog.String("error", err.Error()))
		return OutboundMessage{
			Text:  "Something went wrong checking your plan. Try again in a moment.",
			Speak: "Something went wrong checking your plan. Try again in a moment.",
		}, false
	}
}

// handleUnlinked answers a chat that belongs to no account.
//
// A code is the only thing it may send that has an effect. Everything else gets
// the same instruction, including commands: there is no account to run them
// against.
func (s *Service) handleUnlinked(ctx context.Context, in InboundMessage, text string) (OutboundMessage, error) {
	cmd, arg := parseCommand(text)

	// "/start ABCD1234" is how Telegram delivers a deep link, so a code can
	// arrive as a command argument as easily as on its own.
	candidate := text
	if cmd != cmdNone {
		candidate = arg
	}

	if code := FindCode(candidate); code != "" {
		link, err := s.repo.RedeemCode(ctx, in.Platform, in.ChatID, in.DisplayName, HashCode(code), s.now())
		switch {
		case errors.Is(err, ErrBadCode):
			return OutboundMessage{
				Text:  "That code has expired or was already used. Create a new one in Loci under Settings › Connections.",
				Speak: "That code has expired or was already used. Create a new one in Loci, under Settings and Connections.",
			}, nil
		case err != nil:
			return OutboundMessage{}, err
		}

		s.logger.InfoContext(ctx, "chat linked",
			slog.String("platform", in.Platform),
			slog.String("user_id", link.UserID.String()))
		return OutboundMessage{
			Text:  "Linked. Ask me for an itinerary — try \"three days in Lisbon\" — and it will carry on in the Loci app.",
			Speak: "Linked. Ask me for an itinerary, and it will carry on in the Loci app.",
		}, nil
	}

	// A recording gets the same instruction and is never transcribed. There is
	// nobody to charge it to, and a code cannot be spoken anyway: a transcript
	// of "A3F9C1D2" read aloud is "a three F nine see one D two", in some
	// spelling that cannot be normalised back.
	return OutboundMessage{
		Text:  "This chat is not linked to a Loci account yet. Open Loci, go to Settings › Connections, and send me the code it gives you.",
		Speak: "This chat is not linked to a Loci account yet. Open Loci, go to Settings and Connections, and send me the code it gives you.",
	}, nil
}

// handleCommand runs a command from a linked chat.
//
// None of these reach the model. A command is a fixed answer, and letting
// "/help" cost a generation would be charging somebody's daily quota to read
// instructions.
func (s *Service) handleCommand(ctx context.Context, in InboundMessage, link Link, cmd command, _ string) (OutboundMessage, error) {
	switch cmd {
	case cmdStart:
		return OutboundMessage{
			Text:  "You are already linked. Ask me for an itinerary — try \"a weekend in Porto\".",
			Speak: "You are already linked. Ask me for an itinerary — try a weekend in Porto.",
		}, nil

	case cmdHelp:
		return OutboundMessage{Speak: "Ask me for an itinerary in plain language. What you ask here and what you ask in the Loci app are the same conversation.", Text: strings.Join([]string{
			"Ask me for an itinerary in plain language — \"three days in Lisbon, we like food and old buildings\".",
			"",
			"What you ask here and what you ask in the Loci app are the same conversation.",
			"",
			"/unlink — disconnect this chat from your Loci account.",
		}, "\n")}, nil

	case cmdUnlink:
		if err := s.repo.UnlinkChat(ctx, in.Platform, in.ChatID); err != nil && !errors.Is(err, ErrNotLinked) {
			return OutboundMessage{}, err
		}
		s.logger.InfoContext(ctx, "chat unlinked",
			slog.String("platform", in.Platform),
			slog.String("user_id", link.UserID.String()))
		return OutboundMessage{
			Text:  "Disconnected. Your trips and history are untouched — only this chat was unlinked.",
			Speak: "Disconnected. Your trips and history are untouched — only this chat was unlinked.",
		}, nil

	default:
		// Unreachable: Handle does not route unrecognised commands here, so
		// that they fall through to the answerer instead.
		return OutboundMessage{}, nil
	}
}
