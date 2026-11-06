package locitypes

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// MobilityLevel is how the traveller gets around on foot. It names a
// functional need, never a diagnosis. Stored as a string so the JSONB column
// stays readable and survives proto renumbering.
type MobilityLevel string

const (
	MobilityUnspecified      MobilityLevel = ""
	MobilityNone             MobilityLevel = "none"
	MobilityLimitedWalking   MobilityLevel = "limited_walking"
	MobilityWalkingAid       MobilityLevel = "walking_aid"
	MobilityManualWheelchair MobilityLevel = "manual_wheelchair"
	MobilityPowerWheelchair  MobilityLevel = "power_wheelchair"
	MobilityScooter          MobilityLevel = "mobility_scooter"
)

// UsesWheels reports whether the traveller moves on wheels and so needs
// step-free, kerb-aware routing.
func (m MobilityLevel) UsesWheels() bool {
	switch m {
	case MobilityManualWheelchair, MobilityPowerWheelchair, MobilityScooter:
		return true
	default:
		return false
	}
}

// MaxIncline is the steepest slope the traveller can manage.
type MaxIncline string

const (
	MaxInclineUnspecified MaxIncline = ""
	MaxInclineAny         MaxIncline = "any"
	MaxIncline3           MaxIncline = "3"
	MaxIncline6           MaxIncline = "6"
	MaxIncline10          MaxIncline = "10"
)

// Percent is the incline as a whole percentage, or 0 when there is no limit.
func (m MaxIncline) Percent() int {
	switch m {
	case MaxIncline3:
		return 3
	case MaxIncline6:
		return 6
	case MaxIncline10:
		return 10
	default:
		return 0
	}
}

// AccessibilityNeeds are the traveller's self-described access needs.
//
// This is special-category (health) data under GDPR Article 9: it is stored
// only with explicit consent, must never be logged or sent to analytics, and
// is dropped entirely when the traveller clears it. LogValue below keeps it
// out of slog output even when a whole profile is logged by mistake.
type AccessibilityNeeds struct {
	Mobility                MobilityLevel `json:"mobility,omitempty"`
	MaxWalkMinutesPerLeg    int           `json:"max_walk_minutes_per_leg,omitempty"`
	MaxWalkMinutesPerDay    int           `json:"max_walk_minutes_per_day,omitempty"`
	StepFreeOnly            bool          `json:"step_free_only,omitempty"`
	MaxIncline              MaxIncline    `json:"max_incline,omitempty"`
	RestEveryMinutes        int           `json:"rest_every_minutes,omitempty"`
	NeedsSeatingAtStops     bool          `json:"needs_seating_at_stops,omitempty"`
	NeedsAccessibleToilet   bool          `json:"needs_accessible_toilet,omitempty"`
	AvoidCobbles            bool          `json:"avoid_cobbles,omitempty"`
	LowVision               bool          `json:"low_vision,omitempty"`
	Blind                   bool          `json:"blind,omitempty"`
	DeafOrHardOfHearing     bool          `json:"deaf_or_hard_of_hearing,omitempty"`
	SensorySensitive        bool          `json:"sensory_sensitive,omitempty"`
	PrefersQuietTimes       bool          `json:"prefers_quiet_times,omitempty"`
	NeedsPredictablePlan    bool          `json:"needs_predictable_plan,omitempty"`
	FatigueOrChronicPain    bool          `json:"fatigue_or_chronic_pain,omitempty"`
	ServiceAnimal           bool          `json:"service_animal,omitempty"`
	Stroller                bool          `json:"stroller,omitempty"`
	TravellingWithCompanion bool          `json:"travelling_with_companion,omitempty"`
	Notes                   string        `json:"notes,omitempty"`

	// ConsentAt is when the traveller agreed to Loci storing these needs. It
	// lives in its own column, not in the JSON blob, so it is excluded here.
	ConsentAt *time.Time `json:"-"`
}

// Accessibility needs limits, mirrored from the proto's protovalidate rules so
// the domain layer rejects bad values even when a caller skips validation.
const (
	maxWalkPerLegLimit = 120
	maxWalkPerDayLimit = 600
	maxRestEveryLimit  = 240
	maxNotesLen        = 500
)

// Validate checks the ranges the proto declares.
func (a *AccessibilityNeeds) Validate() error {
	if a == nil {
		return nil
	}
	switch a.Mobility {
	case MobilityUnspecified, MobilityNone, MobilityLimitedWalking, MobilityWalkingAid,
		MobilityManualWheelchair, MobilityPowerWheelchair, MobilityScooter:
	default:
		return fmt.Errorf("%w: unknown mobility level %q", ErrBadRequest, a.Mobility)
	}
	switch a.MaxIncline {
	case MaxInclineUnspecified, MaxInclineAny, MaxIncline3, MaxIncline6, MaxIncline10:
	default:
		return fmt.Errorf("%w: unknown max incline %q", ErrBadRequest, a.MaxIncline)
	}
	if a.MaxWalkMinutesPerLeg < 0 || a.MaxWalkMinutesPerLeg > maxWalkPerLegLimit {
		return fmt.Errorf("%w: max_walk_minutes_per_leg must be 0-%d", ErrBadRequest, maxWalkPerLegLimit)
	}
	if a.MaxWalkMinutesPerDay < 0 || a.MaxWalkMinutesPerDay > maxWalkPerDayLimit {
		return fmt.Errorf("%w: max_walk_minutes_per_day must be 0-%d", ErrBadRequest, maxWalkPerDayLimit)
	}
	if a.RestEveryMinutes < 0 || a.RestEveryMinutes > maxRestEveryLimit {
		return fmt.Errorf("%w: rest_every_minutes must be 0-%d", ErrBadRequest, maxRestEveryLimit)
	}
	if len([]rune(a.Notes)) > maxNotesLen {
		return fmt.Errorf("%w: notes must be at most %d characters", ErrBadRequest, maxNotesLen)
	}
	return nil
}

// Active reports whether the needs say anything at all. An all-default value
// is treated as "no needs", so it neither changes prompts nor cache keys.
func (a *AccessibilityNeeds) Active() bool {
	if a == nil {
		return false
	}
	cp := *a
	cp.ConsentAt = nil
	if cp.Mobility == MobilityNone {
		cp.Mobility = MobilityUnspecified
	}
	if cp.MaxIncline == MaxInclineAny {
		cp.MaxIncline = MaxInclineUnspecified
	}
	return cp != AccessibilityNeeds{}
}

// NeedsStepFree reports whether routes and entrances must avoid steps.
func (a *AccessibilityNeeds) NeedsStepFree() bool {
	if a == nil {
		return false
	}
	return a.StepFreeOnly || a.Mobility.UsesWheels() || a.Stroller
}

// LogValue redacts the needs from structured logs. Logging whether needs are
// set is fine; logging what they are is not.
func (a *AccessibilityNeeds) LogValue() slog.Value {
	if a == nil || !a.Active() {
		return slog.StringValue("none")
	}
	return slog.StringValue("[redacted]")
}

var _ slog.LogValuer = (*AccessibilityNeeds)(nil)

// AccessibilityNoteSeverity is how much a note affects the traveller's plan.
type AccessibilityNoteSeverity string

const (
	AccessibilitySeverityInfo    AccessibilityNoteSeverity = "info"
	AccessibilitySeverityCaution AccessibilityNoteSeverity = "caution"
	AccessibilitySeverityBarrier AccessibilityNoteSeverity = "barrier"
)

// AccessibilityNoteSource is where a note's claim comes from. Only
// AccessibilitySourceAIEstimate is unverified.
type AccessibilityNoteSource string

const (
	AccessibilitySourceVerifiedCrowd AccessibilityNoteSource = "verified_crowd"
	AccessibilitySourceOpenData      AccessibilityNoteSource = "open_data"
	AccessibilitySourceRouted        AccessibilityNoteSource = "routed"
	AccessibilitySourceAIEstimate    AccessibilityNoteSource = "ai_estimate"
)

// AccessibilityNote is one access fact or caveat about a place or a leg.
type AccessibilityNote struct {
	Text          string                    `json:"text"`
	Severity      AccessibilityNoteSeverity `json:"severity,omitempty"`
	Source        AccessibilityNoteSource   `json:"source,omitempty"`
	SourceLicence string                    `json:"source_licence,omitempty"`
	ObservedAt    *time.Time                `json:"observed_at,omitempty"`
}

// Limits mirrored from AccessibilityNote's protovalidate rules.
const (
	MaxAccessibilityNotes      = 10
	maxAccessibilityNoteLength = 300
)

// SanitizeModelAccessibilityNotes cleans notes a model wrote. Whatever source
// the model claims, its notes are its own estimate: only Loci's data paths may
// mark a note verified, open data or routed. Empty notes are dropped, unknown
// severities become caution (the safe reading of an unclear warning), text is
// cut to the wire limit, and the list is capped.
func SanitizeModelAccessibilityNotes(notes []AccessibilityNote) []AccessibilityNote {
	if len(notes) == 0 {
		return nil
	}
	out := make([]AccessibilityNote, 0, min(len(notes), MaxAccessibilityNotes))
	for _, n := range notes {
		text := strings.TrimSpace(n.Text)
		if text == "" {
			continue
		}
		if r := []rune(text); len(r) > maxAccessibilityNoteLength {
			text = strings.TrimSpace(string(r[:maxAccessibilityNoteLength-1])) + "…"
		}
		severity := AccessibilityNoteSeverity(strings.ToLower(strings.TrimSpace(string(n.Severity))))
		switch severity {
		case AccessibilitySeverityInfo, AccessibilitySeverityCaution, AccessibilitySeverityBarrier:
		default:
			severity = AccessibilitySeverityCaution
		}
		out = append(out, AccessibilityNote{
			Text:     text,
			Severity: severity,
			Source:   AccessibilitySourceAIEstimate,
		})
		if len(out) == MaxAccessibilityNotes {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SanitizeModelPOINotes applies SanitizeModelAccessibilityNotes to every POI
// in place.
func SanitizeModelPOINotes(pois []POIDetailedInfo) {
	for i := range pois {
		pois[i].AccessibilityNotes = SanitizeModelAccessibilityNotes(pois[i].AccessibilityNotes)
	}
}
