package presenter

import (
	"testing"
	"time"

	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func TestToProtoAccessibilityNotes(t *testing.T) {
	observed := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	got := ToProtoAccessibilityNotes([]locitypes.AccessibilityNote{
		{Text: "Step-free entrance", Severity: locitypes.AccessibilitySeverityInfo, Source: locitypes.AccessibilitySourceOpenData, SourceLicence: "ODbL-1.0", ObservedAt: &observed},
		{Text: ""},
		{Text: "Stairs only", Severity: locitypes.AccessibilitySeverityBarrier, Source: "made-up"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d notes, want 2 (empty dropped)", len(got))
	}
	if got[0].GetSource() != poiv1.AccessibilityNoteSource_ACCESSIBILITY_NOTE_SOURCE_OPEN_DATA ||
		got[0].GetSourceLicence() != "ODbL-1.0" || !got[0].GetObservedAt().AsTime().Equal(observed) {
		t.Errorf("open-data note lost provenance: %v", got[0])
	}
	if got[1].GetSource() != poiv1.AccessibilityNoteSource_ACCESSIBILITY_NOTE_SOURCE_AI_ESTIMATE {
		t.Errorf("unknown source must degrade to AI estimate, got %v", got[1].GetSource())
	}
	if got[1].GetSeverity() != poiv1.AccessibilityNoteSeverity_ACCESSIBILITY_NOTE_SEVERITY_BARRIER {
		t.Errorf("severity lost: %v", got[1].GetSeverity())
	}

	poi := ToPOIDetailedInfo(locitypes.POIDetailedInfo{
		Name:               "Castelo",
		AccessibilityNotes: []locitypes.AccessibilityNote{{Text: "Steep climb", Severity: locitypes.AccessibilitySeverityCaution}},
	})
	if len(poi.GetAccessibilityNotes()) != 1 {
		t.Fatal("ToPOIDetailedInfo dropped accessibility notes")
	}
}
