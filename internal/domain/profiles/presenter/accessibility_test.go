package presenter

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func fullProtoNeeds() *profilev1.AccessibilityNeeds {
	return &profilev1.AccessibilityNeeds{
		Mobility:                profilev1.MobilityLevel_MOBILITY_LEVEL_MANUAL_WHEELCHAIR,
		MaxWalkMinutesPerLeg:    10,
		MaxWalkMinutesPerDay:    90,
		StepFreeOnly:            true,
		MaxIncline:              profilev1.MaxIncline_MAX_INCLINE_PCT_6,
		RestEveryMinutes:        45,
		NeedsSeatingAtStops:     true,
		NeedsAccessibleToilet:   true,
		AvoidCobbles:            true,
		LowVision:               true,
		Blind:                   true,
		DeafOrHardOfHearing:     true,
		SensorySensitive:        true,
		PrefersQuietTimes:       true,
		NeedsPredictablePlan:    true,
		FatigueOrChronicPain:    true,
		ServiceAnimal:           true,
		Stroller:                true,
		TravellingWithCompanion: true,
		Notes:                   "Transfers need a firm seat",
	}
}

// Every field must survive the round trip; a field dropped here is a need the
// AI silently never hears about.
func TestAccessibilityNeedsRoundTrip(t *testing.T) {
	in := fullProtoNeeds()
	consentAt := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	domain := FromProtoAccessibilityNeeds(in)
	domain.ConsentAt = &consentAt
	out := ToProtoAccessibilityNeeds(domain)

	want := proto.Clone(in).(*profilev1.AccessibilityNeeds)
	want.ConsentAt = timestamppb.New(consentAt)
	if !proto.Equal(out, want) {
		t.Fatalf("round trip mismatch:\n got  %v\n want %v", out, want)
	}
}

func TestAccessibilityNeedsIgnoresClientConsentAt(t *testing.T) {
	in := fullProtoNeeds()
	in.ConsentAt = timestamppb.Now()
	if got := FromProtoAccessibilityNeeds(in); got.ConsentAt != nil {
		t.Fatal("client-supplied consent_at must be ignored")
	}
}

func TestAccessibilityNeedsNilAndInactive(t *testing.T) {
	if FromProtoAccessibilityNeeds(nil) != nil {
		t.Fatal("nil proto must map to nil (not supplied)")
	}
	if ToProtoAccessibilityNeeds(nil) != nil {
		t.Fatal("nil domain must map to nil")
	}
	if ToProtoAccessibilityNeeds(&locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityNone}) != nil {
		t.Fatal("inactive needs must be omitted")
	}
}

func TestFromUpdateProtoCarriesAccessibility(t *testing.T) {
	params, err := FromUpdateProto(&profilev1.UpdateUserPreferenceProfileRequest{
		ProfileId:                 "p",
		AccessibilityNeeds:        fullProtoNeeds(),
		AccessibilityConsentGiven: proto.Bool(true),
		ClearAccessibilityNeeds:   proto.Bool(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if params.AccessibilityNeeds == nil || !params.AccessibilityConsentGiven || !params.ClearAccessibilityNeeds {
		t.Fatalf("update params dropped accessibility fields: %+v", params)
	}

	params, err = FromUpdateProto(&profilev1.UpdateUserPreferenceProfileRequest{ProfileId: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if params.AccessibilityNeeds != nil || params.ClearAccessibilityNeeds {
		t.Fatal("absent needs must leave stored needs alone")
	}
}

func TestFromCreateProtoCarriesAccessibility(t *testing.T) {
	params, err := FromCreateProto(&profilev1.CreateUserPreferenceProfileRequest{
		ProfileName:               "Lisbon",
		AccessibilityNeeds:        fullProtoNeeds(),
		AccessibilityConsentGiven: proto.Bool(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if params.AccessibilityNeeds == nil || !params.AccessibilityConsentGiven {
		t.Fatalf("create params dropped accessibility fields: %+v", params)
	}
}
