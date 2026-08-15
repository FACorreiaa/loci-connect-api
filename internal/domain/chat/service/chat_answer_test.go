package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func msg(role locitypes.MessageRole, content string) locitypes.ConversationMessage {
	return locitypes.ConversationMessage{Role: role, Content: content}
}

func TestRenderConversationHistoryEmptyIsEmpty(t *testing.T) {
	if got := renderConversationHistory(nil); got != "" {
		t.Errorf("nil history rendered %q, want empty", got)
	}
	blank := []locitypes.ConversationMessage{msg(locitypes.RoleUser, "   ")}
	if got := renderConversationHistory(blank); got != "" {
		t.Errorf("whitespace-only history rendered %q, want empty", got)
	}
}

func TestRenderConversationHistoryKeepsTheTail(t *testing.T) {
	var history []locitypes.ConversationMessage
	for i := 0; i < maxHistoryTurns+5; i++ {
		history = append(history, msg(locitypes.RoleUser, "message-"+string(rune('a'+i))))
	}

	got := renderConversationHistory(history)

	// The most recent turns carry the live constraints; the oldest are dropped.
	last := history[len(history)-1].Content
	if !strings.Contains(got, last) {
		t.Errorf("history omitted the most recent message %q", last)
	}
	first := history[0].Content
	if strings.Contains(got, first) {
		t.Errorf("history replayed the oldest message %q past the bound", first)
	}
	if n := strings.Count(got, "message-"); n != maxHistoryTurns {
		t.Errorf("replayed %d messages, want %d", n, maxHistoryTurns)
	}
}

func TestRenderConversationHistoryLabelsSpeakers(t *testing.T) {
	got := renderConversationHistory([]locitypes.ConversationMessage{
		msg(locitypes.RoleUser, "where should I eat?"),
		msg(locitypes.RoleAssistant, "try the market"),
	})

	if !strings.Contains(got, "User: where should I eat?") {
		t.Errorf("user turn mislabelled:\n%s", got)
	}
	if !strings.Contains(got, "You: try the market") {
		t.Errorf("assistant turn mislabelled:\n%s", got)
	}
}

func TestRenderConversationHistoryTruncatesLongMessages(t *testing.T) {
	long := strings.Repeat("x", maxHistoryCharsPerMessage*3)

	got := renderConversationHistory([]locitypes.ConversationMessage{msg(locitypes.RoleUser, long)})

	if len(got) > maxHistoryCharsPerMessage*2 {
		t.Errorf("one long message was not truncated: rendered %d chars", len(got))
	}
	if !strings.Contains(got, "…") {
		t.Error("truncation marker missing")
	}
}

func TestRenderItinerarySummaryNumbersPlaces(t *testing.T) {
	session := &locitypes.ChatSession{
		CurrentItinerary: &locitypes.AiCityResponse{
			AIItineraryResponse: locitypes.AIItineraryResponse{
				PointsOfInterest: []locitypes.POIDetailedInfo{
					{Name: "Time Out Market", Category: "food hall"},
					{Name: "Torre de Belém"},
				},
			},
		},
	}

	got := renderItinerarySummary(session)

	if !strings.Contains(got, "1. Time Out Market (food hall)") {
		t.Errorf("first stop rendered wrong:\n%s", got)
	}
	// A place with no category must not render an empty pair of brackets.
	if !strings.Contains(got, "2. Torre de Belém\n") {
		t.Errorf("uncategorised stop rendered wrong:\n%s", got)
	}
}

func TestRenderItinerarySummaryHandlesNoItinerary(t *testing.T) {
	if got := renderItinerarySummary(nil); got != "" {
		t.Errorf("nil session rendered %q", got)
	}
	if got := renderItinerarySummary(&locitypes.ChatSession{}); got != "" {
		t.Errorf("session with no itinerary rendered %q", got)
	}
}

func TestBuildQuestionPromptCombinesEverySource(t *testing.T) {
	poiID := uuid.New()
	session := &locitypes.ChatSession{
		SessionContext:      locitypes.SessionContext{CityName: "Lisbon"},
		ConversationHistory: []locitypes.ConversationMessage{msg(locitypes.RoleUser, "planning three days")},
		CurrentItinerary: &locitypes.AiCityResponse{
			AIItineraryResponse: locitypes.AIItineraryResponse{
				PointsOfInterest: []locitypes.POIDetailedInfo{{Name: "Torre de Belém"}},
			},
		},
	}
	packet := &retrieval.ContextPacket{
		PacketID: "pkt_x",
		Evidence: []retrieval.Evidence{{POIID: poiID, Name: "Time Out Market", Category: "food hall"}},
	}

	got := buildQuestionPrompt("is the market open on Monday?", session, packet)

	for _, want := range []string{
		"Lisbon",
		"planning three days",          // history
		"Torre de Belém",               // itinerary
		"[poi:" + poiID.String() + "]", // evidence
		"is the market open on Monday?",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// The instruction that matters most for a question: refusing to guess.
	if !strings.Contains(got, "do not guess") {
		t.Errorf("prompt does not forbid guessing:\n%s", got)
	}
}

func TestBuildQuestionPromptWorksWithoutOptionalContext(t *testing.T) {
	session := &locitypes.ChatSession{
		SessionContext: locitypes.SessionContext{CityName: "Porto"},
	}

	got := buildQuestionPrompt("what is there to do?", session, nil)

	if !strings.Contains(got, "Porto") || !strings.Contains(got, "what is there to do?") {
		t.Errorf("bare prompt lost its essentials:\n%s", got)
	}
	if strings.Contains(got, "CONVERSATION SO FAR") {
		t.Error("empty history rendered a heading with nothing under it")
	}
	if strings.Contains(got, "CURRENT ITINERARY") {
		t.Error("absent itinerary rendered a heading with nothing under it")
	}
}
