package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Progress stages are shown verbatim after the assistant's name in the chat
// header ("Loci is searching places"), so they must be human verb phrases and
// never the raw event types or status codes the service uses internally.
func TestProgressStage(t *testing.T) {
	cases := []struct {
		name  string
		event locitypes.StreamEvent
		want  string
	}{
		{"status code", locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: map[string]any{"status": "generating_semantic_context", "progress": 20}}, "searching places"},
		{"context loaded", locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: map[string]any{"status": "context_loaded", "city_id": uuid.NewString()}}, "loading your trip"},
		{"no results", locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: map[string]any{"status": "no_pois_found"}}, "widening the search"},
		{"typed event with status", locitypes.StreamEvent{Type: "semantic_context_generated", Data: map[string]any{"status": "semantic_context_ready"}}, "matching places to you"},
		{"string-map status with a place name", locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: map[string]string{"status": "Getting details for Mercado dos Lavradores..."}}, "getting place details"},
		{"plain string data", locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Processing: Updating itinerary..."}, "updating your itinerary"},
		{"plain string sorting", locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Data: "Sorting updated POIs by distance..."}, "sorting by distance"},
		{"hotels domain", locitypes.StreamEvent{Type: locitypes.EventTypeDomainDetected, Data: map[string]any{"domain": "accommodation"}}, "checking hotels"},
		{"itinerary domain", locitypes.StreamEvent{Type: locitypes.EventTypeDomainDetected, Data: map[string]any{"domain": "itinerary"}}, "planning your day"},
		{"session validated keeps no raw status", locitypes.StreamEvent{Type: "session_validated", Data: map[string]string{"status": "active"}}, "getting started"},
		{"intent classified", locitypes.StreamEvent{Type: "intent_classified", Data: map[string]string{"intent": "add_poi"}}, "reading your request"},
		{"unknown status falls back to the type", locitypes.StreamEvent{Type: "poi_added_successfully", Data: map[string]any{"status": "brand_new_code"}}, "updating your itinerary"},
		{"unknown everything", locitypes.StreamEvent{Type: "something_new", Data: map[string]any{"status": "also_new"}}, defaultProgressStage},
		{"nil data, empty type", locitypes.StreamEvent{}, defaultProgressStage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := progressStage(tc.event); got != tc.want {
				t.Fatalf("progressStage = %q, want %q", got, tc.want)
			}
		})
	}
}

// Every stage in every table must fit the header and read as prose.
func TestProgressStageCopyRules(t *testing.T) {
	stages := []string{defaultProgressStage}
	for _, s := range progressStatusStages {
		stages = append(stages, s)
	}
	for _, s := range progressTypeStages {
		stages = append(stages, s)
	}
	for _, s := range progressDomainStages {
		stages = append(stages, s)
	}
	for _, s := range progressTextStages {
		stages = append(stages, s.stage)
	}
	for _, s := range stages {
		if s == "" || len(s) > maxProgressStageLen {
			t.Errorf("stage %q: length %d, want 1..%d", s, len(s), maxProgressStageLen)
		}
		if strings.ContainsAny(s, "_.:") || s != strings.ToLower(s) {
			t.Errorf("stage %q: want a lower-case verb phrase without codes or punctuation", s)
		}
	}
}

// The handler's collapse-to-progress path must carry the human stage.
func TestMapEventToProto_ProgressCarriesHumanStage(t *testing.T) {
	h := &ChatHandler{}
	resp, err := h.mapEventToProto(context.Background(), locitypes.StreamEvent{
		Type: locitypes.EventTypeProgress,
		Data: map[string]any{"status": "analyzing_semantic_matches", "semantic_options": 3},
	}, uuid.New())
	if err != nil {
		t.Fatalf("mapEventToProto: %v", err)
	}
	p := resp.GetProgress()
	if p == nil {
		t.Fatalf("expected a progress payload, got %T", resp.GetPayload())
	}
	if p.GetStage() != "comparing places" {
		t.Fatalf("Stage = %q, want %q", p.GetStage(), "comparing places")
	}
}
