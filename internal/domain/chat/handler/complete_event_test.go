package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The completion event carries only navigation data ({session_id, trip_id}).
// Decoding that map into AiCityResponse used to yield a zero-valued Result,
// which the client took as the final payload and used to wipe the real data
// it had already rendered.
func TestMapEventToProto_CompleteWithoutResult(t *testing.T) {
	h := &ChatHandler{}
	userID := uuid.New()
	sessionID := uuid.New()

	t.Run("navigation-only completion carries the session id and no result", func(t *testing.T) {
		event := locitypes.StreamEvent{
			Type:      locitypes.EventTypeComplete,
			Data:      map[string]any{"session_id": sessionID.String(), "trip_id": ""},
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}
		resp, err := h.mapEventToProto(context.Background(), event, userID)
		if err != nil {
			t.Fatalf("mapEventToProto: %v", err)
		}
		cp := resp.GetComplete()
		if cp == nil {
			t.Fatalf("expected a complete payload, got %T", resp.GetPayload())
		}
		if cp.GetSessionId() != sessionID.String() {
			t.Fatalf("SessionId = %q, want %q", cp.GetSessionId(), sessionID)
		}
		if cp.GetResult() != nil {
			t.Fatalf("expected no result on a navigation-only completion, got %+v", cp.GetResult())
		}
	})

	t.Run("completion with a real city response keeps the result", func(t *testing.T) {
		event := locitypes.StreamEvent{
			Type: locitypes.EventTypeComplete,
			Data: locitypes.AiCityResponse{
				SessionID:       sessionID,
				GeneralCityData: locitypes.GeneralCityData{City: "Funchal", Country: "Portugal"},
			},
			Timestamp: time.Now(),
			EventID:   uuid.New().String(),
		}
		resp, err := h.mapEventToProto(context.Background(), event, userID)
		if err != nil {
			t.Fatalf("mapEventToProto: %v", err)
		}
		cp := resp.GetComplete()
		if cp == nil {
			t.Fatalf("expected a complete payload, got %T", resp.GetPayload())
		}
		if cp.GetSessionId() != sessionID.String() {
			t.Fatalf("SessionId = %q, want %q", cp.GetSessionId(), sessionID)
		}
		if cp.GetResult() == nil {
			t.Fatal("expected the result to be kept when the response has content")
		}
		if got := cp.GetResult().GetGeneralCityData().GetCity(); got != "Funchal" {
			t.Fatalf("Result.GeneralCityData.City = %q, want %q", got, "Funchal")
		}
	})
}

func TestAiCityResponseHasContent(t *testing.T) {
	cases := []struct {
		name string
		cr   locitypes.AiCityResponse
		want bool
	}{
		{name: "zero value", want: false},
		{name: "only a session id", cr: locitypes.AiCityResponse{SessionID: uuid.New()}, want: false},
		{name: "city", cr: locitypes.AiCityResponse{GeneralCityData: locitypes.GeneralCityData{City: "Funchal"}}, want: true},
		{name: "pois", cr: locitypes.AiCityResponse{PointsOfInterest: []locitypes.POIDetailedInfo{{Name: "x"}}}, want: true},
		{name: "itinerary pois", cr: locitypes.AiCityResponse{AIItineraryResponse: locitypes.AIItineraryResponse{PointsOfInterest: []locitypes.POIDetailedInfo{{Name: "x"}}}}, want: true},
		{name: "hotels", cr: locitypes.AiCityResponse{Hotels: []locitypes.HotelDetailedInfo{{Name: "x"}}}, want: true},
		{name: "restaurants", cr: locitypes.AiCityResponse{Restaurants: []locitypes.RestaurantDetailedInfo{{Name: "x"}}}, want: true},
		{name: "activities", cr: locitypes.AiCityResponse{Activities: []locitypes.POIDetailedInfo{{Name: "x"}}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := aiCityResponseHasContent(&tc.cr); got != tc.want {
				t.Fatalf("aiCityResponseHasContent = %v, want %v", got, tc.want)
			}
		})
	}
}

// The nearby path emits a classified "no_results" error; the transport must
// map it to a retryable typed error without touching the user-facing copy.
func TestStreamErrorFromEventNoResults(t *testing.T) {
	const msg = "Unable to find places near your location. Please try again or expand your search radius."
	se := streamErrorFromEvent(locitypes.StreamEvent{
		Type:      locitypes.EventTypeError,
		Error:     msg,
		ErrorCode: locitypes.StreamErrorNoResults,
	})
	if se.GetInternalCode() != "no_results" {
		t.Fatalf("InternalCode = %q, want %q", se.GetInternalCode(), "no_results")
	}
	if !se.GetRetryable() {
		t.Fatal("expected no_results to be retryable")
	}
	if se.GetUserMessage() != msg {
		t.Fatalf("UserMessage = %q, want it unchanged", se.GetUserMessage())
	}
}
