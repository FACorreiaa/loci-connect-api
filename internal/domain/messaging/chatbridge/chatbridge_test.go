package chatbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

type fakeChat struct {
	sessions []locitypes.ChatSession
	listErr  error

	continued    uuid.UUID
	continueErr  error
	started      bool
	startErr     error
	continueText string
	startText    string
	reply        string

	// callerSeen is whoever the context said was calling, as the provider
	// router downstream would read it.
	callerSeen string
}

func (f *fakeChat) StartChat(ctx context.Context, _, _ uuid.UUID, _, message string, _ *locitypes.UserLocation) (*locitypes.ChatResponse, error) {
	f.started = true
	f.startText = message
	f.callerSeen, _ = interceptors.GetUserIDFromContext(ctx)
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &locitypes.ChatResponse{SessionID: uuid.New(), Message: f.replyOr("a new plan"), IsNewSession: true}, nil
}

func (f *fakeChat) ContinueChat(ctx context.Context, _, sessionID uuid.UUID, message, _ string) (*locitypes.ChatResponse, error) {
	f.continued = sessionID
	f.continueText = message
	f.callerSeen, _ = interceptors.GetUserIDFromContext(ctx)
	if f.continueErr != nil {
		return nil, f.continueErr
	}
	return &locitypes.ChatResponse{SessionID: sessionID, Message: f.replyOr("an updated plan")}, nil
}

func (f *fakeChat) GetUserChatSessions(context.Context, uuid.UUID, int, int) (*locitypes.ChatSessionsResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &locitypes.ChatSessionsResponse{Sessions: f.sessions, Total: len(f.sessions)}, nil
}

func (f *fakeChat) replyOr(fallback string) string {
	if f.reply != "" {
		return f.reply
	}
	return fallback
}

// The promise of the integration: a question from a phone continues the
// conversation the app was having.
func TestAMessageContinuesTheMostRecentConversation(t *testing.T) {
	existing := uuid.New()
	chat := &fakeChat{sessions: []locitypes.ChatSession{{ID: existing}}}

	got, err := New(chat, nil).Answer(t.Context(), uuid.New(), "make day two quieter")
	if err != nil {
		t.Fatalf("answer: %v", err)
	}

	if chat.continued != existing {
		t.Error("a new session was started instead of continuing the open one")
	}
	if chat.started {
		t.Error("StartChat was called as well")
	}
	if chat.continueText != "make day two quieter" {
		t.Errorf("the chat service received %q", chat.continueText)
	}
	if got != "an updated plan" {
		t.Errorf("reply = %q", got)
	}
}

func TestTheFirstMessageStartsAConversation(t *testing.T) {
	chat := &fakeChat{}

	got, err := New(chat, nil).Answer(t.Context(), uuid.New(), "three days in Lisbon")
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !chat.started {
		t.Fatal("no conversation was started")
	}
	if got != "a new plan" {
		t.Errorf("reply = %q", got)
	}
}

// A session can expire between being listed and being used. That is ordinary,
// and the sender should get an itinerary rather than an apology.
func TestAnExpiredSessionFallsBackToStartingOne(t *testing.T) {
	chat := &fakeChat{
		sessions:    []locitypes.ChatSession{{ID: uuid.New()}},
		continueErr: errors.New("session expired"),
	}

	got, err := New(chat, nil).Answer(t.Context(), uuid.New(), "three days in Lisbon")
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !chat.started {
		t.Fatal("the expired session was not replaced with a new one")
	}
	if got == "" {
		t.Error("no reply")
	}
}

// Failing to read the session list is not a reason to refuse to answer.
func TestAFailureToListSessionsStillAnswers(t *testing.T) {
	chat := &fakeChat{listErr: errors.New("database is having a moment")}

	if _, err := New(chat, nil).Answer(t.Context(), uuid.New(), "three days in Lisbon"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !chat.started {
		t.Error("nothing was asked of the chat service")
	}
}

// The itinerary is in the app; an empty chat bubble is not an answer.
func TestAnEmptyModelReplyStillSaysSomething(t *testing.T) {
	chat := &fakeChat{reply: "   "}

	got, err := New(chat, nil).Answer(t.Context(), uuid.New(), "three days in Lisbon")
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if got == "" {
		t.Fatal("the reply is empty")
	}
}

func TestNothingToAnswerIsAnError(t *testing.T) {
	if _, err := New(&fakeChat{}, nil).Answer(t.Context(), uuid.New(), "   "); err == nil {
		t.Error("an empty message was answered")
	}
	if _, err := New(nil, nil).Answer(t.Context(), uuid.New(), "hello"); err == nil {
		t.Error("a message was answered with no chat service")
	}
}

// If StartChat fails there is nothing left to try.
func TestAFailureToStartIsReported(t *testing.T) {
	chat := &fakeChat{startErr: errors.New("no provider")}

	if _, err := New(chat, nil).Answer(t.Context(), uuid.New(), "three days in Lisbon"); err == nil {
		t.Error("a failure to start a conversation was swallowed")
	}
}

// A chat message carries no JWT. The provider router reads the caller from the
// context to pick their own key and their plan's chain, so without this every
// Telegram message would run on Loci's shared provider regardless of what the
// account brought or pays for.
func TestTheRequestActsAsTheLinkedAccount(t *testing.T) {
	userID := uuid.New()

	t.Run("starting", func(t *testing.T) {
		chat := &fakeChat{}
		if _, err := New(chat, nil).Answer(t.Context(), userID, "three days in Lisbon"); err != nil {
			t.Fatalf("answer: %v", err)
		}
		if chat.callerSeen != userID.String() {
			t.Errorf("the chat service saw caller %q, want the linked account %s", chat.callerSeen, userID)
		}
	})

	t.Run("continuing", func(t *testing.T) {
		chat := &fakeChat{sessions: []locitypes.ChatSession{{ID: uuid.New()}}}
		if _, err := New(chat, nil).Answer(t.Context(), userID, "make day two quieter"); err != nil {
			t.Fatalf("answer: %v", err)
		}
		if chat.callerSeen != userID.String() {
			t.Errorf("the chat service saw caller %q, want the linked account %s", chat.callerSeen, userID)
		}
	})
}
