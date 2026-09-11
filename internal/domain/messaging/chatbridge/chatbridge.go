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
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
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
//
// Prose wins when the model wrote any: it is the answer to what was asked.
// With none, the itinerary itself is rendered rather than announced — a
// traveller who asked in a chat wants the plan in that chat, not a pointer to
// a browser. The client splits anything past Telegram's limit, so length here
// costs extra messages rather than a truncated plan.
func reply(response *locitypes.ChatResponse) string {
	if response == nil {
		return "Done — open Loci to see it."
	}
	if text := strings.TrimSpace(response.Message); text != "" {
		return text
	}
	if itinerary := renderItinerary(response.UpdatedItinerary); itinerary != "" {
		return itinerary
	}
	// Nothing to render: the turn produced neither prose nor a plan.
	return "Done — open Loci to see it."
}

// maxRenderedPOIs caps how many places each section of a chat reply carries.
//
// A long reply is split across messages rather than cut, so the cap is about
// what a person will read in a chat, not about Telegram's limit.
const maxRenderedPOIs = 12

// renderItinerary turns a plan into plain text, or "" when there is none.
//
// Two sections: the itinerary's own stops, then the rest of the city worth
// seeing. A traveller asked one question and wants both answers — the plan to
// follow, and what else is around — without opening anything.
func renderItinerary(city *locitypes.AiCityResponse) string {
	if city == nil {
		return ""
	}

	plan := city.AIItineraryResponse

	var b strings.Builder
	if name := strings.TrimSpace(plan.ItineraryName); name != "" {
		b.WriteString(name)
		b.WriteString("\n\n")
	}
	if overview := strings.TrimSpace(plan.OverallDescription); overview != "" {
		b.WriteString(overview)
		b.WriteString("\n\n")
	}

	itinerary := byDistance(plan.PointsOfInterest)
	writePOIs(&b, itinerary)

	// The general list repeats the plan's places often enough to be worth
	// filtering: the same name twice in one message reads as a mistake.
	seen := make(map[string]struct{}, len(itinerary))
	for _, poi := range itinerary {
		seen[dedupKey(poi.Name)] = struct{}{}
	}
	var rest []locitypes.POIDetailedInfo
	for _, poi := range city.PointsOfInterest {
		if _, dup := seen[dedupKey(poi.Name)]; dup {
			continue
		}
		rest = append(rest, poi)
	}
	if len(rest) > 0 {
		if b.Len() > 0 {
			b.WriteString("\nAlso in the city\n")
		}
		writePOIs(&b, byDistance(rest))
	}

	return strings.TrimSpace(b.String())
}

// dedupKey compares places by the name a reader sees, so the same place cited
// in one list and not the other is still recognised as the same place.
func dedupKey(name string) string {
	clean, _, _ := retrieval.StripCitation(name)
	return strings.ToLower(strings.TrimSpace(clean))
}

// byDistance orders places nearest first.
//
// A distance of zero means "not known" rather than "at the centre" — the
// field is optional and an ungrounded answer leaves it empty — so those keep
// their original order at the end instead of claiming the closest spots.
func byDistance(pois []locitypes.POIDetailedInfo) []locitypes.POIDetailedInfo {
	ordered := make([]locitypes.POIDetailedInfo, len(pois))
	copy(ordered, pois)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].Distance, ordered[j].Distance
		switch {
		case a <= 0 && b <= 0:
			return false // both unknown: leave them as they came
		case a <= 0:
			return false // unknown sorts after anything measured
		case b <= 0:
			return true
		default:
			return a < b
		}
	})
	return ordered
}

// writePOIs appends one section of places, capped and counted.
func writePOIs(b *strings.Builder, pois []locitypes.POIDetailedInfo) {
	for i, poi := range pois {
		if i == maxRenderedPOIs {
			fmt.Fprintf(b, "\n…and %d more in Loci.\n", len(pois)-maxRenderedPOIs)
			return
		}
		// A grounded answer carries its [poi:<uuid>] citation in the name.
		// The reader gets the name; the id is what a map link would use.
		name, _, _ := retrieval.StripCitation(poi.Name)
		if name == "" {
			continue
		}
		b.WriteString("• ")
		b.WriteString(name)
		if category := strings.TrimSpace(poi.Category); category != "" {
			b.WriteString(" — ")
			b.WriteString(category)
		}
		if poi.Distance > 0 {
			fmt.Fprintf(b, " (%.1f km)", poi.Distance)
		}
		// DescriptionPOI is the itinerary-specific note; Description is the
		// POI's general one. Either reads better than the name alone.
		detail := strings.TrimSpace(poi.DescriptionPOI)
		if detail == "" {
			detail = strings.TrimSpace(poi.Description)
		}
		if detail != "" {
			b.WriteString("\n  ")
			b.WriteString(detail)
		}
		if link := mapLink(poi); link != "" {
			b.WriteString("\n  ")
			b.WriteString(link)
		}
		if photo := photoLine(poi); photo != "" {
			b.WriteString("\n  ")
			b.WriteString(photo)
		}
		b.WriteString("\n")
	}
}

// photoLine returns a picture's URL and its credit, or "" when the place has no
// picture we are allowed to show.
//
// Telegram previews a bare URL in a plain-text message, so the picture appears
// without sendPhoto, without a second API call, and without the per-message
// rate limit that one photo per place would run into.
//
// The credit is not decoration. These come from Wikimedia Commons under CC BY-SA
// and similar, which require naming the author and the licence wherever the
// image appears — a preview in a chat included. Anything missing either is
// skipped rather than shown uncredited, though the server should never have
// stored such a row in the first place.
func photoLine(poi locitypes.POIDetailedInfo) string {
	for _, img := range poi.ImageCredits {
		url := strings.TrimSpace(img.URL)
		attribution := strings.TrimSpace(img.Attribution)
		licence := strings.TrimSpace(img.Licence)
		if url == "" || attribution == "" || licence == "" {
			continue
		}
		return fmt.Sprintf("%s (%s, %s)", url, attribution, licence)
	}
	return ""
}

// mapLink returns a maps URL for a place, or "" when we do not know where it is.
//
// Only coordinates resolved from the database earn a link. A grounded place
// carries the row's own pin (see resolvePacketPOIs); an ungrounded one carries
// whatever the model guessed, and a link built on a guess sends a traveller to
// the wrong street with full confidence. No link is the honest answer there.
//
// One link, not two: replies are split at Telegram's limit and the parts are
// sent back to back with no pacing, so every extra line is a real cost. Google
// Maps opens in the browser or the app on every platform, including iOS.
func mapLink(poi locitypes.POIDetailedInfo) string {
	if !poi.Grounded || (poi.Latitude == 0 && poi.Longitude == 0) {
		return ""
	}
	return fmt.Sprintf("https://www.google.com/maps/search/?api=1&query=%.6f,%.6f",
		poi.Latitude, poi.Longitude)
}
