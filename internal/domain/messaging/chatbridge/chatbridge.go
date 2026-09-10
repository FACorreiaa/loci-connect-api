// Package chatbridge answers chat-platform messages with the same conversation
// the web app uses.
//
// It exists so a question asked in Telegram continues in the Loci app and back:
// both go through one chat session rather than two histories that happen to
// belong to the same person. That is the whole promise of the Telegram
// integration, and it is one function.
package chatbridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Chat is the part of the chat service this bridge uses.
//
// An interface rather than the concrete service so this package can be tested
// without building the whole provider chain, and so it states exactly what a
// chat platform needs: continue a conversation, or start one.
type Chat interface {
	StartChat(ctx context.Context, userID, profileID uuid.UUID, cityName, message string, userLocation *locitypes.UserLocation) (*locitypes.ChatResponse, error)
	ContinueChat(ctx context.Context, userID, sessionID uuid.UUID, message, cityName string) (*locitypes.ChatResponse, error)
	GetUserChatSessions(ctx context.Context, userID uuid.UUID, page, limit int) (*locitypes.ChatSessionsResponse, error)
}

// Answerer turns a message from a linked chat into a reply.
type Answerer struct {
	chat   Chat
	logger *slog.Logger
}

func New(chat Chat, logger *slog.Logger) *Answerer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Answerer{chat: chat, logger: logger}
}

// Answer continues the user's most recent conversation, or starts one.
//
// Continuing is the default on purpose. Somebody who asked for three days in
// Lisbon in the app and then sends "make day two quieter" from their phone
// means the same trip; starting a new session would answer a question nobody
// asked. A message that cannot be continued starts a session rather than
// failing, so the reply is an itinerary rather than an apology.
func (a *Answerer) Answer(ctx context.Context, userID uuid.UUID, text string) (string, error) {
	if a.chat == nil {
		return "", errors.New("chatbridge: no chat service configured")
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("chatbridge: nothing to answer")
	}

	// A chat message arrives with no JWT, so nothing below here would know
	// whose request this is: the provider router reads the caller from the
	// context to pick their own key and their plan's chain, and with none it
	// serves Loci's shared provider to everyone. The link already resolved
	// the account; this is what makes the rest of the request act as it.
	ctx = interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: userID.String()})

	if sessionID, ok := a.latestSession(ctx, userID); ok {
		response, err := a.chat.ContinueChat(ctx, userID, sessionID, text, "")
		if err == nil {
			return reply(response), nil
		}
		// The session may have expired between listing it and using it, which
		// is ordinary rather than exceptional. Fall through and start a new
		// one instead of reporting a failure the sender cannot act on.
		a.logger.InfoContext(ctx, "could not continue the existing chat session, starting a new one",
			slog.String("user_id", userID.String()),
			slog.String("error", err.Error()))
	}

	// No profile and no location: a chat platform has neither. The service
	// applies the account's own defaults, which is what the app would use.
	response, err := a.chat.StartChat(ctx, userID, uuid.Nil, "", text, nil)
	if err != nil {
		return "", fmt.Errorf("chatbridge: %w", err)
	}
	return reply(response), nil
}

// latestSession finds the conversation this message should continue.
func (a *Answerer) latestSession(ctx context.Context, userID uuid.UUID) (uuid.UUID, bool) {
	// One session: the most recent is the one a follow-up refers to.
	sessions, err := a.chat.GetUserChatSessions(ctx, userID, 1, 1)
	if err != nil {
		a.logger.InfoContext(ctx, "could not read chat sessions, starting a new one",
			slog.String("user_id", userID.String()),
			slog.String("error", err.Error()))
		return uuid.Nil, false
	}
	if sessions == nil || len(sessions.Sessions) == 0 {
		return uuid.Nil, false
	}

	session := sessions.Sessions[0]
	if session.ID == uuid.Nil {
		return uuid.Nil, false
	}
	return session.ID, true
}

// reply extracts the text a chat platform can send.
func reply(response *locitypes.ChatResponse) string {
	if response == nil || strings.TrimSpace(response.Message) == "" {
		// The itinerary is in the app; the model returned no prose to go with
		// it. Saying so is better than sending an empty message.
		return "Done — open Loci to see it."
	}
	return response.Message
}
