// Package watch is Loci's standing tasks: things a user asked the agent to
// keep an eye on ("every morning at 8, tell me if it will rain in Lisbon").
//
// The flow is propose → confirm. ProposeWatch turns free text into a
// Proposal the client shows as a card; CreateWatch stores the confirmed
// proposal against a chat thread. A Runner started from cmd/ wakes every
// minute, and — on whichever replica holds the advisory lock — runs each due
// watch through the LLM and appends the answer to its thread as a proactive
// assistant message captioned "Standing task".
package watch

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// SourceLabel captions every message a watch posts.
const SourceLabel = "Standing task"

// Interval bounds, mirrored by the proto validation and the table's CHECK.
const (
	MinIntervalMinutes = 60
	MaxIntervalMinutes = 43200
)

// MaxWatchesPerUser bounds what one account can make the runner spend.
const MaxWatchesPerUser = 10

var (
	ErrNotFound      = errors.New("watch: not found")
	ErrInvalid       = errors.New("watch: invalid proposal")
	ErrLimitReached  = errors.New("watch: too many standing tasks")
	ErrUnknownZone   = errors.New("watch: unknown time zone")
	ErrSessionAbsent = errors.New("watch: chat session not found")
)

// Proposal is what the server understood from the user's text.
type Proposal struct {
	Title           string
	ScheduleHuman   string
	IntervalMinutes int
	Spec            string
	// FirstRunAt is nil when the first run is one interval from creation.
	FirstRunAt *time.Time
}

// Watch is a stored, confirmed proposal.
type Watch struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	SessionID       uuid.UUID
	Title           string
	ScheduleHuman   string
	IntervalMinutes int
	Spec            string
	Enabled         bool
	NextRunAt       time.Time
	LastRunAt       *time.Time
	CreatedAt       time.Time
}

// Interval is the watch's period.
func (w Watch) Interval() time.Duration {
	return time.Duration(w.IntervalMinutes) * time.Minute
}

// Repository stores watches.
type Repository interface {
	Create(ctx context.Context, w Watch) (Watch, error)
	CountByUser(ctx context.Context, userID uuid.UUID) (int, error)
	// List returns the user's watches, soonest next run first. A nil
	// sessionID lists every thread.
	List(ctx context.Context, userID uuid.UUID, sessionID *uuid.UUID) ([]Watch, error)
	// Delete reports ErrNotFound when no watch with id belongs to userID.
	Delete(ctx context.Context, userID, id uuid.UUID) error
	// ClaimDue returns up to limit enabled watches due at now and, in the
	// same transaction, moves each one's next_run_at to its next future
	// slot and stamps last_run_at. A watch is therefore claimed at most once
	// per slot even if two runners overlap; a run that then fails simply
	// waits for its next slot.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]Watch, error)
	Disable(ctx context.Context, id uuid.UUID) error
}

// Sessions is the slice of the chat repository a watch needs: read the
// thread it belongs to, and append to it. chat/repository.RepositoryImpl
// satisfies it.
type Sessions interface {
	GetSession(ctx context.Context, sessionID uuid.UUID) (*locitypes.ChatSession, error)
	AddMessageToSession(ctx context.Context, sessionID uuid.UUID, message locitypes.ConversationMessage) error
}

// TextGenerator is the one LLM call a watch run makes. The go-genai-sdk
// ChatClient satisfies it.
type TextGenerator interface {
	GenerateText(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (string, error)
}

// Notifier is told when a watch has posted into a thread. cityName is the
// thread's city, for a deep link back to it. PushNotifier announces it on
// the user's iPhones; without one the message is only in the thread.
type Notifier interface {
	WatchPosted(ctx context.Context, w Watch, cityName string, msg locitypes.ConversationMessage)
}

type noopNotifier struct{}

func (noopNotifier) WatchPosted(context.Context, Watch, string, locitypes.ConversationMessage) {}

// nextSlot is the first time strictly after now on w's schedule, counting
// from its current next_run_at. Missed slots (the server was down) are
// skipped rather than replayed.
func nextSlot(from time.Time, interval time.Duration, now time.Time) time.Time {
	if interval <= 0 {
		interval = MinIntervalMinutes * time.Minute
	}
	next := from.Add(interval)
	if !next.After(now) {
		missed := now.Sub(next)/interval + 1
		next = next.Add(missed * interval)
	}
	return next
}
