package recents

import (
	"testing"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	recentsv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recents"
)

// The stored prompt is the string that was sent to the model, wrapper and all.
// The feed shows what the person typed, so the wrapper comes off here rather
// than in every client that ever reads the feed.
func TestUnwrapPrompt(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"wrapped", "Unified Chat Stream - Domain: itinerary, Message: Three days in Porto", "Three days in Porto"},
		{"multiline message survives", "Unified Chat Stream - Domain: general, Message: first\nsecond", "first\nsecond"},
		{"unwrapped is left alone", "Return ONLY a JSON object", "Return ONLY a JSON object"},
		{"a message quoting the wrapper is not eaten", "why does it say Unified Chat Stream - Domain: x, Message: y", "why does it say Unified Chat Stream - Domain: x, Message: y"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unwrapPrompt(tc.in); got != tc.want {
				t.Errorf("unwrapPrompt(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// entity_type is what the client routes on, and entity_id is what it navigates
// with. Getting either wrong sends a feed row to the wrong page.
func TestActivityEntryToProto(t *testing.T) {
	cases := []struct {
		name           string
		entry          locitypes.ActivityEntry
		wantType       recentsv1.InteractionType
		wantEntityType string
		wantDesc       string
	}{
		{
			name:           "a general chat turn",
			entry:          locitypes.ActivityEntry{Kind: locitypes.ActivityKindPrompt, Detail: "general", RefID: "sess", Label: "Unified Chat Stream - Domain: general, Message: hello"},
			wantType:       recentsv1.InteractionType_INTERACTION_TYPE_CHAT,
			wantEntityType: "general",
			wantDesc:       "hello",
		},
		{
			name:           "an itinerary request",
			entry:          locitypes.ActivityEntry{Kind: locitypes.ActivityKindPrompt, Detail: "itinerary", Label: "Unified Chat Stream - Domain: itinerary, Message: two days in Braga"},
			wantType:       recentsv1.InteractionType_INTERACTION_TYPE_SEARCH,
			wantEntityType: "itinerary",
			wantDesc:       "two days in Braga",
		},
		{
			name:           "a nearby search",
			entry:          locitypes.ActivityEntry{Kind: locitypes.ActivityKindPrompt, Detail: "nearby", Label: "Unified Chat Stream - Domain: nearby, Message: what is around me"},
			wantType:       recentsv1.InteractionType_INTERACTION_TYPE_DISCOVERY,
			wantEntityType: "nearby",
			wantDesc:       "what is around me",
		},
		{
			name:           "a kept trip",
			entry:          locitypes.ActivityEntry{Kind: locitypes.ActivityKindSavedItinerary, Detail: "itinerary", Label: "Porto weekend"},
			wantType:       recentsv1.InteractionType_INTERACTION_TYPE_SAVE_ITINERARY,
			wantEntityType: "saved_itinerary",
			wantDesc:       "Porto weekend",
		},
		{
			name:           "a favourited place",
			entry:          locitypes.ActivityEntry{Kind: locitypes.ActivityKindFavourite, Detail: "restaurant", Label: "Cervejaria Ramiro"},
			wantType:       recentsv1.InteractionType_INTERACTION_TYPE_FAVORITE,
			wantEntityType: "favourite",
			wantDesc:       "Cervejaria Ramiro",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := activityEntryToProto("user-1", tc.entry)
			if got.GetInteractionType() != tc.wantType {
				t.Errorf("interaction_type = %v, want %v", got.GetInteractionType(), tc.wantType)
			}
			if got.GetEntityType() != tc.wantEntityType {
				t.Errorf("entity_type = %q, want %q", got.GetEntityType(), tc.wantEntityType)
			}
			if got.GetDescription() != tc.wantDesc {
				t.Errorf("description = %q, want %q", got.GetDescription(), tc.wantDesc)
			}
			if got.GetMetadata()["kind"] != string(tc.entry.Kind) {
				t.Errorf("metadata kind = %q, want %q", got.GetMetadata()["kind"], tc.entry.Kind)
			}
		})
	}

	fav := activityEntryToProto("user-1", locitypes.ActivityEntry{Kind: locitypes.ActivityKindFavourite, Detail: "hotel", Label: "Some Hotel"})
	if fav.GetMetadata()["content_type"] != "hotel" {
		t.Errorf("a favourite must carry its content type beside entity_type, got %q", fav.GetMetadata()["content_type"])
	}
}

// The RPC has one repeated-string filter field and the feed has two axes, so
// entity_types carries both. Anything naming a feed kind narrows the kind;
// everything else narrows the detail. That is what lets "itinerary searches"
// and "saved itineraries" be different requests.
func TestActivityFilterFromProto(t *testing.T) {
	got := activityFilterFromProto(&recentsv1.InteractionFilter{
		EntityTypes: []string{"prompt", "itinerary"},
	}, "")
	if len(got.Kinds) != 1 || got.Kinds[0] != "prompt" {
		t.Errorf("Kinds = %v, want [prompt]", got.Kinds)
	}
	if len(got.Details) != 1 || got.Details[0] != "itinerary" {
		t.Errorf("Details = %v, want [itinerary]", got.Details)
	}
	if got.Ascending {
		t.Error("an unset sort order must stay newest-first")
	}

	asc := activityFilterFromProto(nil, "asc")
	if !asc.Ascending {
		t.Error("sort_order asc was ignored")
	}

	enum := activityFilterFromProto(&recentsv1.InteractionFilter{
		InteractionTypes: []recentsv1.InteractionType{
			recentsv1.InteractionType_INTERACTION_TYPE_FAVORITE,
			recentsv1.InteractionType_INTERACTION_TYPE_CHAT,
			recentsv1.InteractionType_INTERACTION_TYPE_SEARCH,
		},
	}, "desc")
	if len(enum.Kinds) != 2 {
		t.Errorf("Kinds = %v, want favourite and prompt exactly once each", enum.Kinds)
	}
}
