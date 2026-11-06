package locitypes

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestAccessibilityNeedsActive(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		needs *AccessibilityNeeds
		want  bool
	}{
		{"nil", nil, false},
		{"zero", &AccessibilityNeeds{}, false},
		{"explicit none and any", &AccessibilityNeeds{Mobility: MobilityNone, MaxIncline: MaxInclineAny}, false},
		{"consent alone is not a need", &AccessibilityNeeds{ConsentAt: &now}, false},
		{"wheelchair", &AccessibilityNeeds{Mobility: MobilityManualWheelchair}, true},
		{"quiet times", &AccessibilityNeeds{PrefersQuietTimes: true}, true},
		{"notes only", &AccessibilityNeeds{Notes: "I use a cane on bad days"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.needs.Active(); got != tc.want {
				t.Fatalf("Active() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAccessibilityNeedsValidate(t *testing.T) {
	valid := &AccessibilityNeeds{
		Mobility: MobilityScooter, MaxIncline: MaxIncline6,
		MaxWalkMinutesPerLeg: 120, MaxWalkMinutesPerDay: 600, RestEveryMinutes: 240,
		Notes: strings.Repeat("é", 500),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid needs rejected: %v", err)
	}
	var nilNeeds *AccessibilityNeeds
	if err := nilNeeds.Validate(); err != nil {
		t.Fatalf("nil needs rejected: %v", err)
	}

	bad := []*AccessibilityNeeds{
		{Mobility: "crawling"},
		{MaxIncline: "45"},
		{MaxWalkMinutesPerLeg: -1},
		{MaxWalkMinutesPerLeg: 121},
		{MaxWalkMinutesPerDay: 601},
		{RestEveryMinutes: 241},
		{Notes: strings.Repeat("a", 501)},
	}
	for i, n := range bad {
		if err := n.Validate(); !errors.Is(err, ErrBadRequest) {
			t.Errorf("case %d: Validate() = %v, want ErrBadRequest", i, err)
		}
	}
}

func TestAccessibilityNeedsStepFree(t *testing.T) {
	for _, n := range []*AccessibilityNeeds{
		{StepFreeOnly: true},
		{Mobility: MobilityPowerWheelchair},
		{Mobility: MobilityScooter},
		{Stroller: true},
	} {
		if !n.NeedsStepFree() {
			t.Errorf("%+v: NeedsStepFree() = false", n)
		}
	}
	if (&AccessibilityNeeds{Mobility: MobilityWalkingAid}).NeedsStepFree() {
		t.Error("walking aid alone should not force step-free")
	}
}

// The needs are GDPR special-category data. A profile logged by mistake must
// not leak them.
func TestAccessibilityNeedsNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	needs := &AccessibilityNeeds{Mobility: MobilityManualWheelchair, Notes: "secret-note"}
	logger.Info("profile", slog.Any("needs", needs))
	out := buf.String()
	if strings.Contains(out, "secret-note") || strings.Contains(out, "manual_wheelchair") {
		t.Fatalf("needs leaked into log: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected redaction marker, got %s", out)
	}
}

func TestSanitizeModelAccessibilityNotes(t *testing.T) {
	long := strings.Repeat("x", 400)
	in := []AccessibilityNote{
		{Text: "  Step-free entrance on the east side  ", Severity: "INFO", Source: AccessibilitySourceVerifiedCrowd},
		{Text: "", Severity: "barrier"},
		{Text: "Steep cobbled lane", Severity: "dangerous"},
		{Text: long, Severity: "barrier", Source: AccessibilitySourceRouted},
	}
	got := SanitizeModelAccessibilityNotes(in)
	if len(got) != 3 {
		t.Fatalf("got %d notes, want 3 (empty dropped): %+v", len(got), got)
	}
	for _, n := range got {
		if n.Source != AccessibilitySourceAIEstimate {
			t.Errorf("model note kept source %q; a model can never verify access", n.Source)
		}
	}
	if got[0].Text != "Step-free entrance on the east side" || got[0].Severity != AccessibilitySeverityInfo {
		t.Errorf("note 0 not trimmed/normalised: %+v", got[0])
	}
	if got[1].Severity != AccessibilitySeverityCaution {
		t.Errorf("unknown severity should become caution, got %q", got[1].Severity)
	}
	if n := len([]rune(got[2].Text)); n != 300 {
		t.Errorf("long note is %d runes, want 300", n)
	}

	many := make([]AccessibilityNote, 25)
	for i := range many {
		many[i] = AccessibilityNote{Text: "note"}
	}
	if got := SanitizeModelAccessibilityNotes(many); len(got) != MaxAccessibilityNotes {
		t.Errorf("got %d notes, want cap %d", len(got), MaxAccessibilityNotes)
	}
	if SanitizeModelAccessibilityNotes([]AccessibilityNote{{Text: "  "}}) != nil {
		t.Error("all-empty input should sanitise to nil")
	}
}

// A note the model wrote into its JSON reaches POIDetailedInfo through the
// normal decoder, which is the path the stream parser uses.
func TestPOIDetailedInfoDecodesAccessibilityNotes(t *testing.T) {
	var poi POIDetailedInfo
	raw := `{"name":"Castelo","accessibility_notes":[{"text":"Steep climb to the gate","severity":"caution","source":"verified_crowd"}]}`
	if err := json.Unmarshal([]byte(raw), &poi); err != nil {
		t.Fatal(err)
	}
	if len(poi.AccessibilityNotes) != 1 || poi.AccessibilityNotes[0].Text != "Steep climb to the gate" {
		t.Fatalf("notes not decoded: %+v", poi.AccessibilityNotes)
	}
	pois := []POIDetailedInfo{poi}
	SanitizeModelPOINotes(pois)
	if pois[0].AccessibilityNotes[0].Source != AccessibilitySourceAIEstimate {
		t.Fatal("SanitizeModelPOINotes left a model-claimed verified source")
	}
}
