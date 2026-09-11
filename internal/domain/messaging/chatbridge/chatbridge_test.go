package chatbridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestAPlanWithoutProseIsSentAsText(t *testing.T) {
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			ItineraryName:      "Four days in Madeira",
			OverallDescription: "Levadas, wine and the old town.",
			PointsOfInterest: []locitypes.POIDetailedInfo{
				{Name: "Pico do Arieiro", Category: "Viewpoint", Description: "Above the clouds."},
				{Name: "Blandy's Wine Lodge", Category: "Winery"},
			},
		},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	for _, want := range []string{"Four days in Madeira", "Pico do Arieiro", "Viewpoint", "Above the clouds.", "Blandy's Wine Lodge"} {
		if !strings.Contains(got, want) {
			t.Errorf("the reply does not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "open Loci") {
		t.Errorf("a rendered plan should not fall back to the pointer text:\n%s", got)
	}
}

func TestBothThePlanAndTheRestOfTheCityAreSent(t *testing.T) {
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{{Name: "Sé Cathedral", Distance: 0.2}},
		},
		PointsOfInterest: []locitypes.POIDetailedInfo{
			{Name: "Sé Cathedral", Distance: 0.2}, // already in the plan
			{Name: "Porto Moniz Pools", Distance: 45.0},
		},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	if !strings.Contains(got, "Porto Moniz Pools") {
		t.Errorf("the general city places are missing:\n%s", got)
	}
	if strings.Count(got, "Sé Cathedral") != 1 {
		t.Errorf("a place in both lists should be sent once:\n%s", got)
	}
}

func TestPlacesAreOrderedNearestFirst(t *testing.T) {
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{
				{Name: "Far", Distance: 2.5},
				{Name: "Unknown"}, // no distance
				{Name: "Near", Distance: 0.2},
				{Name: "Middle", Distance: 0.4},
			},
		},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	var order []int
	for _, name := range []string{"Near", "Middle", "Far", "Unknown"} {
		order = append(order, strings.Index(got, name))
	}
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Fatalf("places are not nearest-first (unknown distance last):\n%s", got)
		}
	}
	if !strings.Contains(got, "(0.2 km)") {
		t.Errorf("a measured distance should be shown:\n%s", got)
	}
	if strings.Contains(got, "Unknown — ") || strings.Contains(got, "Unknown (") {
		t.Errorf("an unknown distance should not be printed:\n%s", got)
	}
}

func TestAPlanLongerThanTheCapPointsAtTheApp(t *testing.T) {
	pois := make([]locitypes.POIDetailedInfo, maxRenderedPOIs+3)
	for i := range pois {
		pois[i] = locitypes.POIDetailedInfo{Name: fmt.Sprintf("Place %d", i)}
	}
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{PointsOfInterest: pois},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	if !strings.Contains(got, "and 3 more in Loci.") {
		t.Errorf("the reply does not say what was left out:\n%s", got)
	}
	if strings.Contains(got, fmt.Sprintf("Place %d", maxRenderedPOIs)) {
		t.Errorf("the reply went past the cap:\n%s", got)
	}
}

func TestAnAnswerWithNeitherProseNorPlanStillSaysSomething(t *testing.T) {
	if got := reply(&locitypes.ChatResponse{}); got == "" {
		t.Fatal("the reply is empty")
	}
}

func TestACitationIsNotShownToTheReader(t *testing.T) {
	const cited = "Ribeira Brava Town Center [poi:cef674e9-9aeb-41e6-88cb-595d63869f6d]"
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{{Name: cited, Distance: 1.2}},
		},
		PointsOfInterest: []locitypes.POIDetailedInfo{
			{Name: cited, Distance: 1.2}, // same place, cited in both lists
			{Name: "Cabo Girão", Distance: 3.0},
		},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	if strings.Contains(got, "poi:") || strings.Contains(got, "[") {
		t.Errorf("the citation marker reached the reader:\n%s", got)
	}
	if !strings.Contains(got, "Ribeira Brava Town Center") {
		t.Errorf("the name did not survive stripping:\n%s", got)
	}
	if strings.Count(got, "Ribeira Brava Town Center") != 1 {
		t.Errorf("a cited place in both lists should be sent once:\n%s", got)
	}
}

func TestOnlyAGroundedPlaceGetsAMapLink(t *testing.T) {
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{
				{Name: "Cabo Girão", Grounded: true, Latitude: 32.6325, Longitude: -17.0015, Distance: 0.5},
				{Name: "Somewhere Imagined", Latitude: 12.3, Longitude: 4.5, Distance: 1.0},
			},
		},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	if !strings.Contains(got, "google.com/maps/search/?api=1&query=32.632500,-17.001500") {
		t.Errorf("a grounded place should carry its pin:\n%s", got)
	}
	if strings.Contains(got, "12.3") {
		t.Errorf("an ungrounded place must not be linked by a guessed coordinate:\n%s", got)
	}
	if strings.Count(got, "google.com/maps") != 1 {
		t.Errorf("exactly one link expected:\n%s", got)
	}
}

func TestAPictureIsSentWithItsCredit(t *testing.T) {
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{{
				Name: "Cabo Girão",
				ImageCredits: []locitypes.POIImage{{
					URL:         "https://upload.wikimedia.org/cabo-girao.jpg",
					Attribution: "H. Zell",
					Licence:     "CC BY-SA 3.0",
				}},
			}},
		},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	if !strings.Contains(got, "https://upload.wikimedia.org/cabo-girao.jpg") {
		t.Errorf("the picture is missing:\n%s", got)
	}
	// The licence requires the author and the licence wherever the image shows,
	// and a Telegram preview is the image showing.
	if !strings.Contains(got, "H. Zell") || !strings.Contains(got, "CC BY-SA 3.0") {
		t.Errorf("the credit did not travel with the picture:\n%s", got)
	}
}

func TestAnUncreditedPictureIsNotSent(t *testing.T) {
	plan := &locitypes.AiCityResponse{
		AIItineraryResponse: locitypes.AIItineraryResponse{
			PointsOfInterest: []locitypes.POIDetailedInfo{{
				Name: "Somewhere",
				ImageCredits: []locitypes.POIImage{
					{URL: "https://example.test/no-credit.jpg", Licence: "CC BY-SA 3.0"}, // no author
					{URL: "https://example.test/credited.jpg", Attribution: "Someone", Licence: "CC BY 2.0"},
				},
			}},
		},
	}

	got := reply(&locitypes.ChatResponse{UpdatedItinerary: plan})

	if strings.Contains(got, "no-credit.jpg") {
		t.Errorf("an uncreditable picture was sent:\n%s", got)
	}
	if !strings.Contains(got, "credited.jpg") {
		t.Errorf("the creditable picture should still be sent:\n%s", got)
	}
}
