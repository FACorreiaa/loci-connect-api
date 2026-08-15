package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// maxHistoryTurns bounds how much of a conversation is replayed into a prompt.
//
// chat_sessions.conversation_history has been appended to on every turn since
// migration 0005 and read back only to render the UI — never into a prompt. So
// the model has never seen what it or the user said a moment earlier, and
// continuation was carried entirely by the structured CurrentItinerary plus a
// single-message intent classifier.
//
// Bounded rather than complete: an unbounded history is a slow, expensive prompt
// that eventually blows the context window, and the tail of a long conversation
// is where the relevant constraints almost always are.
const maxHistoryTurns = 8

// maxHistoryCharsPerMessage truncates any single replayed message. One pasted
// wall of text should not consume the whole history budget.
const maxHistoryCharsPerMessage = 600

// renderConversationHistory formats the tail of a conversation for a prompt.
// Returns the empty string when there is nothing worth replaying, so callers can
// concatenate unconditionally.
func renderConversationHistory(history []locitypes.ConversationMessage) string {
	if len(history) == 0 {
		return ""
	}

	start := 0
	if len(history) > maxHistoryTurns {
		start = len(history) - maxHistoryTurns
	}

	var b strings.Builder
	b.WriteString("**CONVERSATION SO FAR** (oldest first):\n")
	wrote := false
	for _, msg := range history[start:] {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		role := "User"
		if msg.Role != locitypes.RoleUser {
			role = "You"
		}
		fmt.Fprintf(&b, "%s: %s\n", role,
			retrieval.TruncateRunes(content, maxHistoryCharsPerMessage))
		wrote = true
	}
	if !wrote {
		return ""
	}
	return b.String()
}

// renderItinerarySummary lists the places currently on the plan, so a question
// like "how far is the second one from my hotel?" has a referent.
func renderItinerarySummary(session *locitypes.ChatSession) string {
	if session == nil || session.CurrentItinerary == nil {
		return ""
	}
	pois := session.CurrentItinerary.AIItineraryResponse.PointsOfInterest
	if len(pois) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("**THE USER'S CURRENT ITINERARY** (in order):\n")
	for i, p := range pois {
		fmt.Fprintf(&b, "%d. %s", i+1, p.Name)
		if p.Category != "" {
			fmt.Fprintf(&b, " (%s)", p.Category)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// buildQuestionPrompt assembles the prompt for an answered question.
//
// Everything the model is allowed to assert comes from one of three places: the
// retrieved evidence packet, the itinerary the user is actually holding, and
// what was already said. The instruction to admit ignorance is deliberate — for
// a question, "I don't know" is a correct answer and an invented opening time
// is not.
func buildQuestionPrompt(
	question string,
	session *locitypes.ChatSession,
	packet *retrieval.ContextPacket,
) string {
	var b strings.Builder

	fmt.Fprintf(&b, `You are Loci, a travel assistant. Answer the user's question about their trip to %s.

`, session.SessionContext.CityName)

	if history := renderConversationHistory(session.ConversationHistory); history != "" {
		b.WriteString(history)
		b.WriteString("\n")
	}
	if itinerary := renderItinerarySummary(session); itinerary != "" {
		b.WriteString(itinerary)
		b.WriteString("\n")
	}
	if packet != nil {
		b.WriteString(retrieval.Render(packet))
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, `**THE USER'S QUESTION:** %s

**HOW TO ANSWER:**
- Answer in plain prose. No JSON, no markdown code fences.
- Use only the information above. If it does not contain the answer, say so
  plainly and suggest how the user could find out — do not guess at opening
  hours, prices, or availability.
- When you name a place that appears in the verified list, follow it with its
  exact [poi:<id>] marker.
- Be brief. Two or three sentences unless the question genuinely needs more.
`, question)

	return b.String()
}

// packetForSession wraps already-retrieved POIs into an evidence packet for the
// session-continuation path.
//
// That path runs its own retrieval (generateSemanticPOIRecommendations) before
// branching on intent, so the candidates already exist; this only assembles and
// enriches them. Returns nil when grounding is unavailable, which callers treat
// as "answer without an evidence block" rather than as an error.
func (l *ServiceImpl) packetForSession(
	ctx context.Context,
	session *locitypes.ChatSession,
	message string,
	cityID uuid.UUID,
	candidates []locitypes.POIDetailedInfo,
) *retrieval.ContextPacket {
	if l.assembler == nil || len(candidates) == 0 {
		return nil
	}

	reasons := make(map[uuid.UUID]retrieval.MatchReason, len(candidates))
	for _, poi := range candidates {
		reasons[poi.ID] = retrieval.MatchSemantic
	}

	packet, err := l.assembler.Assemble(ctx, retrieval.Request{
		UserID:     session.UserID,
		Query:      message,
		CityID:     cityID,
		CityName:   session.SessionContext.CityName,
		Candidates: candidates,
		Reasons:    reasons,
	})
	if err != nil {
		l.logger.WarnContext(ctx, "could not assemble evidence for session answer",
			slog.Any("error", err))
		return nil
	}
	return packet
}

// answerQuestionStreamed answers a question from retrieved evidence and streams
// the reply.
//
// This replaces a hardcoded string. The IntentAskQuestion branch used to return
// "I'm here to help! ... What specifically would you like to know?" without
// calling a model at all — so a user who asked a direct question got a prompt to
// ask it again, while the semantic POIs retrieved moments earlier went unused.
func (l *ServiceImpl) answerQuestionStreamed(
	ctx context.Context,
	session *locitypes.ChatSession,
	question string,
	packet *retrieval.ContextPacket,
	eventCh chan<- locitypes.StreamEvent,
) (string, error) {
	prompt := buildQuestionPrompt(question, session, packet)

	release, err := l.acquireLLMSlot(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire llm slot: %w", err)
	}
	defer release()

	config := &genai.GenerateContentConfig{Temperature: genai.Ptr[float32](defaultTemperature)}
	iter, err := l.aiClient.GenerateStream(ctx, prompt, config)
	if err != nil {
		return "", fmt.Errorf("answer stream init failed: %w", err)
	}

	var b strings.Builder
	for resp, streamErr := range iter {
		if streamErr != nil {
			// Partial text is still worth returning; a truncated real answer
			// beats discarding it for a canned one.
			if b.Len() > 0 {
				break
			}
			return "", fmt.Errorf("answer stream failed: %w", streamErr)
		}
		for _, cand := range resp.Candidates {
			if cand.Content == nil {
				continue
			}
			for _, part := range cand.Content.Parts {
				if part.Text == "" {
					continue
				}
				b.WriteString(part.Text)
				l.sendEvent(ctx, eventCh, locitypes.StreamEvent{
					Type:      locitypes.EventTypeChunk,
					Data:      map[string]any{"part": "answer", "chunk": part.Text},
					Timestamp: time.Now(),
					EventID:   uuid.New().String(),
				}, 3)
			}
		}
	}

	answer := strings.TrimSpace(b.String())
	if answer == "" {
		return "", fmt.Errorf("model returned an empty answer")
	}

	// Verify before the text reaches the user: an invented identifier must not
	// be rendered as a citation, and the marker itself is not for human eyes.
	if packet != nil {
		v := retrieval.Verify(packet, answer)
		if len(v.Unknown) > 0 {
			l.logger.WarnContext(ctx, "answer cited identifiers that were never retrieved",
				slog.Int("fabricated_count", len(v.Unknown)),
				slog.String("packet_id", packet.PacketID))
		}
	}
	return retrieval.StripCitations(answer), nil
}
