package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestMapEventToProto_AccessibleRoute(t *testing.T) {
	h := &ChatHandler{}
	event := locitypes.StreamEvent{
		Type:    locitypes.EventTypeAccessibleRoute,
		EventID: "evt-1",
		Data: locitypes.StreamAccessibleRouteData{
			Day:              2,
			TotalWalkMinutes: 34,
			Warnings:         []string{"Day 2 is long"},
			Legs: []locitypes.WalkingLeg{
				{
					FromPOIID: "a", ToPOIID: "b", DistanceM: 950, DurationMin: 15, AscentM: 40, Routed: true,
					SuggestMode: "taxi",
					Notes:       []locitypes.AccessibilityNote{{Text: "Climbs about 40 m", Severity: "caution", Source: "routed"}},
				},
				{FromPOIID: "b", ToPOIID: "c", DistanceM: 300, DurationMin: 5, SuggestMode: "helicopter"},
			},
		},
	}

	resp, err := h.mapEventToProto(context.Background(), event, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetEventType() != chatv1.StreamEventType_STREAM_EVENT_TYPE_ACCESSIBLE_ROUTE {
		t.Fatalf("event type = %v", resp.GetEventType())
	}
	p := resp.GetAccessibleRoute()
	if p == nil || p.GetDay() != 2 || p.GetTotalWalkMinutes() != 34 || len(p.GetWarnings()) != 1 {
		t.Fatalf("payload = %v", p)
	}
	leg := p.GetLegs()[0]
	if !leg.GetRouted() || leg.GetDurationMin() != 15 || leg.GetSuggestMode() != "taxi" {
		t.Errorf("leg = %v", leg)
	}
	if n := leg.GetNotes(); len(n) != 1 || n[0].GetSource() != poiv1.AccessibilityNoteSource_ACCESSIBILITY_NOTE_SOURCE_ROUTED {
		t.Errorf("notes = %v", n)
	}
	if p.GetLegs()[1].SuggestMode != nil {
		t.Error("a suggest_mode outside the proto's allow-list must be dropped, not sent")
	}
}

func TestAccessibleRouteToProtoClampsToValidation(t *testing.T) {
	legs := make([]locitypes.WalkingLeg, 60)
	got := accessibleRouteToProto(locitypes.StreamAccessibleRouteData{
		Day:      0,
		Legs:     legs,
		Warnings: []string{strings.Repeat("w", 400)},
	})
	if got.GetDay() != 1 {
		t.Errorf("day %d, want clamped to 1", got.GetDay())
	}
	if len(got.GetLegs()) != 50 {
		t.Errorf("legs %d, want capped at 50", len(got.GetLegs()))
	}
	if n := len([]rune(got.GetWarnings()[0])); n != 300 {
		t.Errorf("warning length %d, want 300", n)
	}
}
