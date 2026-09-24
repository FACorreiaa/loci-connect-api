package watch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// maxMessageRunes matches ConversationMessage.content's proto limit.
const maxMessageRunes = 4000

// Service proposes, stores and runs watches.
type Service struct {
	repo     Repository
	sessions Sessions
	gen      TextGenerator // nil: watches are stored but never run
	notifier Notifier
	logger   *slog.Logger
	now      func() time.Time
}

func NewService(repo Repository, sessions Sessions, gen TextGenerator, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:     repo,
		sessions: sessions,
		gen:      gen,
		notifier: noopNotifier{},
		logger:   logger,
		now:      time.Now,
	}
}

// WithNotifier sets who hears about posted messages (Stage D: APNs).
func (s *Service) WithNotifier(n Notifier) *Service {
	if n != nil {
		s.notifier = n
	}
	return s
}

// CanRun reports whether this service has a model to run watches with.
func (s *Service) CanRun() bool { return s.gen != nil }

// Propose reads a proposal out of text. timezone is an IANA name; empty is UTC.
func (s *Service) Propose(_ context.Context, text, timezone string) (Proposal, error) {
	loc := time.UTC
	if timezone != "" {
		l, err := time.LoadLocation(timezone)
		if err != nil {
			return Proposal{}, fmt.Errorf("%w: %q", ErrUnknownZone, timezone)
		}
		loc = l
	}
	return Propose(text, loc, s.now())
}

// Create stores a confirmed proposal against one of the user's threads and
// posts the confirmation into it.
func (s *Service) Create(ctx context.Context, userID, sessionID uuid.UUID, p Proposal) (Watch, locitypes.ConversationMessage, error) {
	if err := validate(p); err != nil {
		return Watch{}, locitypes.ConversationMessage{}, err
	}
	session, err := s.sessions.GetSession(ctx, sessionID)
	if err != nil || session == nil || session.UserID != userID {
		// Someone else's thread reads the same as a missing one.
		return Watch{}, locitypes.ConversationMessage{}, ErrSessionAbsent
	}
	n, err := s.repo.CountByUser(ctx, userID)
	if err != nil {
		return Watch{}, locitypes.ConversationMessage{}, err
	}
	if n >= MaxWatchesPerUser {
		return Watch{}, locitypes.ConversationMessage{}, ErrLimitReached
	}

	now := s.now()
	next := now.Add(time.Duration(p.IntervalMinutes) * time.Minute)
	if p.FirstRunAt != nil && p.FirstRunAt.After(now) {
		next = *p.FirstRunAt
	}
	w, err := s.repo.Create(ctx, Watch{
		UserID:          userID,
		SessionID:       sessionID,
		Title:           strings.TrimSpace(p.Title),
		ScheduleHuman:   strings.TrimSpace(p.ScheduleHuman),
		IntervalMinutes: p.IntervalMinutes,
		Spec:            strings.TrimSpace(p.Spec),
		Enabled:         true,
		NextRunAt:       next,
	})
	if err != nil {
		return Watch{}, locitypes.ConversationMessage{}, err
	}

	msg := proactiveMessage(ConfirmationText(w), now)
	if err := s.sessions.AddMessageToSession(ctx, sessionID, msg); err != nil {
		// The watch exists and will run; only the "got it" line is missing.
		s.logger.WarnContext(ctx, "watch created but confirmation not posted",
			slog.String("watch_id", w.ID.String()), slog.Any("error", err))
	}
	return w, msg, nil
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, sessionID *uuid.UUID) ([]Watch, error) {
	return s.repo.List(ctx, userID, sessionID)
}

func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return s.repo.Delete(ctx, userID, id)
}

// RunDue claims up to limit due watches and runs each. It returns how many
// posted a message.
func (s *Service) RunDue(ctx context.Context, limit int) (int, error) {
	if s.gen == nil {
		return 0, nil
	}
	due, err := s.repo.ClaimDue(ctx, s.now(), limit)
	if err != nil {
		return 0, err
	}
	posted := 0
	for _, w := range due {
		if ctx.Err() != nil {
			break
		}
		runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		err := s.Run(runCtx, w)
		cancel()
		if err != nil {
			s.logger.WarnContext(ctx, "standing task run failed",
				slog.String("watch_id", w.ID.String()), slog.Any("error", err))
			continue
		}
		posted++
	}
	return posted, nil
}

// Run asks the model for this watch's update and appends it to the thread as
// a proactive message.
func (s *Service) Run(ctx context.Context, w Watch) error {
	if s.gen == nil {
		return errors.New("watch: no model configured")
	}
	session, err := s.sessions.GetSession(ctx, w.SessionID)
	if err != nil || session == nil || session.UserID != w.UserID {
		// The thread is gone (or was never theirs); nothing to post into.
		// The FK cascade normally deletes the watch first; this is the belt.
		if dErr := s.repo.Disable(ctx, w.ID); dErr != nil {
			return errors.Join(ErrSessionAbsent, dErr)
		}
		return ErrSessionAbsent
	}

	text, err := s.gen.GenerateText(ctx, runPrompt(w, session.CityName, s.now()), &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.4),
	})
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("watch: model returned nothing")
	}

	msg := proactiveMessage(truncateRunes(text, maxMessageRunes), s.now())
	if err := s.sessions.AddMessageToSession(ctx, w.SessionID, msg); err != nil {
		return fmt.Errorf("append to thread: %w", err)
	}
	s.notifier.WatchPosted(ctx, w, msg)
	return nil
}

func proactiveMessage(content string, at time.Time) locitypes.ConversationMessage {
	return locitypes.ConversationMessage{
		ID:          uuid.New(),
		Role:        locitypes.RoleAssistant,
		Content:     content,
		MessageType: locitypes.TypeResponse,
		Timestamp:   at,
		Origin:      locitypes.OriginProactive,
		SourceLabel: SourceLabel,
	}
}

// ConfirmationText is the "Got it" line posted when a watch is created.
func ConfirmationText(w Watch) string {
	return fmt.Sprintf("Got it — I'll watch “%s” %s and ping you here with what I find.",
		w.Title, lowerFirst(w.ScheduleHuman))
}

func runPrompt(w Watch, city string, now time.Time) string {
	var b strings.Builder
	b.WriteString("You are Loci, a travel companion. The user set up a standing task and it is due now.\n")
	fmt.Fprintf(&b, "Task: %s\n", w.Spec)
	fmt.Fprintf(&b, "Schedule: %s\n", w.ScheduleHuman)
	if city != "" {
		fmt.Fprintf(&b, "The conversation this task belongs to is about: %s\n", city)
	}
	fmt.Fprintf(&b, "Current time (UTC): %s\n", now.UTC().Format(time.RFC1123))
	b.WriteString("Write the update as a short chat message (at most 120 words) addressed to the user. " +
		"Lead with the answer. If you cannot know something for certain (live weather, prices, " +
		"availability), say so plainly and give the best guidance you can instead of inventing facts. " +
		"Do not mention that this is a scheduled task.")
	return b.String()
}

func validate(p Proposal) error {
	switch {
	case strings.TrimSpace(p.Title) == "", utf8.RuneCountInString(p.Title) > 120:
		return fmt.Errorf("%w: title", ErrInvalid)
	case strings.TrimSpace(p.ScheduleHuman) == "", utf8.RuneCountInString(p.ScheduleHuman) > 80:
		return fmt.Errorf("%w: schedule", ErrInvalid)
	case strings.TrimSpace(p.Spec) == "", utf8.RuneCountInString(p.Spec) > maxSpecRunes:
		return fmt.Errorf("%w: spec", ErrInvalid)
	case p.IntervalMinutes < MinIntervalMinutes, p.IntervalMinutes > MaxIntervalMinutes:
		return fmt.Errorf("%w: interval must be %d..%d minutes", ErrInvalid, MinIntervalMinutes, MaxIntervalMinutes)
	}
	return nil
}

func lowerFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}
