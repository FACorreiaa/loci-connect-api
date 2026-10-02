// Package tripcards turns the chat agent's trip proposals into chat-platform
// messages with buttons (Telegram), and applies the one a button names.
package tripcards

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/tripaction"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Sessions finds the user's latest conversation (the chat service).
type Sessions interface {
	GetUserChatSessions(ctx context.Context, userID uuid.UUID, page, limit int) (*locitypes.ChatSessionsResponse, error)
}

// Actions is the trip-action service.
type Actions interface {
	Propose(ctx context.Context, userID, tripID, sessionID uuid.UUID, message string) ([]tripaction.Proposal, error)
	ApplyCurrent(ctx context.Context, userID, proposalID uuid.UUID, option *int) (*trip.Trip, *locitypes.ConversationMessage, error)
	Dismiss(ctx context.Context, userID, proposalID uuid.UUID) error
}

type Planner struct {
	sessions Sessions
	trips    trip.SessionTrips
	actions  Actions
	logger   *slog.Logger
}

func New(sessions Sessions, trips trip.SessionTrips, actions Actions, logger *slog.Logger) *Planner {
	return &Planner{sessions: sessions, trips: trips, actions: actions, logger: logger}
}

var _ messaging.TripPlanner = (*Planner)(nil)

// asUser is the context the chat service expects for a linked chat's user,
// as chatbridge builds it.
func asUser(ctx context.Context, userID uuid.UUID, email string) context.Context {
	return interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: userID.String(), Email: email})
}

// Propose looks for changes only when the user's latest conversation produced
// a trip. Otherwise "4 days in Rome" would be read as a change to whatever
// trip they planned last.
func (p *Planner) Propose(ctx context.Context, userID uuid.UUID, email, text string) ([]messaging.TripCard, error) {
	if !mightAskForChange(text) {
		return nil, nil
	}
	ctx = asUser(ctx, userID, email)
	res, err := p.sessions.GetUserChatSessions(ctx, userID, 1, 1)
	if err != nil || res == nil || len(res.Sessions) == 0 {
		return nil, err
	}
	sessionID := res.Sessions[0].ID
	t, err := p.trips.LatestForSession(ctx, userID, sessionID)
	if errors.Is(err, trip.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	proposals, err := p.actions.Propose(ctx, userID, t.ID, sessionID, text)
	if err != nil || len(proposals) == 0 {
		return nil, err
	}
	cards := make([]messaging.TripCard, 0, len(proposals))
	for _, pr := range proposals {
		cards = append(cards, card(pr))
	}
	return cards, nil
}

// changeWords are what a message asking to change a trip's dates, length,
// stay or flights almost always contains, in English and Portuguese. Matched
// as substrings of the lowercased text, so "hotels" and "re-plan" count.
var changeWords = []string{
	"hotel", "hotéis", "stay", "star", "★", "estrela", "alojamento",
	"flight", "fly", "plane", "airport", "voo", "voar", "aeroporto",
	"day", "night", "week", "date", "dia", "noite", "semana", "data",
	"plan", "longer", "shorter",
	"jan", "feb", "fev", "mar", "apr", "abr", "may", "maio", "jun", "jul",
	"aug", "agosto", "sep", "setembro", "oct", "outubro", "nov", "dec", "dez",
}

// mightAskForChange is a cheap gate before the extraction call, which would
// otherwise run ahead of every answer once a trip exists. It errs towards yes:
// a false yes costs one model call, a false no costs only the cards for a
// message that named no number, date or travel word.
func mightAskForChange(text string) bool {
	lower := strings.ToLower(text)
	if strings.IndexFunc(lower, unicode.IsDigit) >= 0 {
		return true
	}
	for _, w := range changeWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// card is one proposal as plain text and buttons. Hotels list their options
// with a numbered "Stay at N" each; a flight shows its links (Telegram
// previews bare URLs) and saves with one press; the rest confirm.
func card(p tripaction.Proposal) messaging.TripCard {
	var b strings.Builder
	b.WriteString(p.Summary)
	var buttons []messaging.Button
	switch p.Action.Kind {
	case tripaction.KindSearchHotels:
		for i, o := range p.Options {
			fmt.Fprintf(&b, "\n%d. %s", i+1, o.Label)
			if o.Detail != "" {
				b.WriteString(" — " + o.Detail)
			}
			if o.Stay != nil && o.Stay.BookingURL != nil {
				b.WriteString("\n   " + *o.Stay.BookingURL)
			}
			opt := i
			buttons = append(buttons, messaging.Button{Label: fmt.Sprintf("Stay at %d", i+1), Data: messaging.ApplyToken(p.ID, &opt)})
		}
	case tripaction.KindSearchFlights:
		if len(p.Options) > 0 && p.Options[0].Flight != nil {
			for _, l := range p.Options[0].Flight.Links {
				fmt.Fprintf(&b, "\n%s: %s", l.Label, l.URL)
			}
			zero := 0
			buttons = append(buttons, messaging.Button{Label: "Save this flight", Data: messaging.ApplyToken(p.ID, &zero)})
		}
	default:
		buttons = append(buttons, messaging.Button{Label: "Confirm", Data: messaging.ApplyToken(p.ID, nil)})
	}
	buttons = append(buttons, messaging.Button{Label: "Not now", Data: messaging.DismissToken(p.ID)})
	return messaging.TripCard{Text: b.String(), Buttons: buttons}
}

// Apply makes the change and says, in plain words, what happened. final is
// false for the failures that leave the proposal pending (tripaction gives a
// failed change back), where pressing again can still work.
func (p *Planner) Apply(ctx context.Context, userID uuid.UUID, email string, proposalID uuid.UUID, option *int) (string, bool) {
	_, msg, err := p.actions.ApplyCurrent(asUser(ctx, userID, email), userID, proposalID, option)
	switch {
	case err == nil:
		if msg != nil && msg.Content != "" {
			return msg.Content, true
		}
		return "Done.", true
	case errors.Is(err, tripaction.ErrNotPending):
		return "That one was already used or dismissed.", true
	case errors.Is(err, tripaction.ErrExpired):
		return "That suggestion has expired. Ask me again.", true
	case errors.Is(err, tripaction.ErrNotFound), errors.Is(err, trip.ErrNotFound):
		return "I can't find that suggestion any more.", true
	case errors.Is(err, trip.ErrInvalidEdit):
		return "That change can't be made to this trip.", true
	case errors.Is(err, tripaction.ErrNoOption):
		return "Pick one of the options first.", false
	case errors.Is(err, trip.ErrVersionConflict):
		return "The trip changed while I was working on it. Try again.", false
	default:
		p.logger.ErrorContext(ctx, "could not apply a trip change from chat",
			slog.String("user_id", userID.String()), slog.String("error", err.Error()))
		return "Something went wrong making that change. Try again in a moment.", false
	}
}

// Dismiss drops the proposal. A second press, or one already used, reads the same.
func (p *Planner) Dismiss(ctx context.Context, userID, proposalID uuid.UUID) string {
	if err := p.actions.Dismiss(ctx, userID, proposalID); err != nil && !errors.Is(err, tripaction.ErrNotPending) {
		p.logger.WarnContext(ctx, "could not dismiss a trip change", slog.String("error", err.Error()))
	}
	return "Okay, I'll leave that."
}
