package presenter

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// FromProtoAccessibilityNeeds maps the wire needs onto the domain type. A nil
// message maps to nil ("not supplied"). consent_at is server-owned, so any
// value a client sends is ignored.
func FromProtoAccessibilityNeeds(n *profilev1.AccessibilityNeeds) *locitypes.AccessibilityNeeds {
	if n == nil {
		return nil
	}
	return &locitypes.AccessibilityNeeds{
		Mobility:                toMobility(n.GetMobility()),
		MaxWalkMinutesPerLeg:    int(n.GetMaxWalkMinutesPerLeg()),
		MaxWalkMinutesPerDay:    int(n.GetMaxWalkMinutesPerDay()),
		StepFreeOnly:            n.GetStepFreeOnly(),
		MaxIncline:              toMaxIncline(n.GetMaxIncline()),
		RestEveryMinutes:        int(n.GetRestEveryMinutes()),
		NeedsSeatingAtStops:     n.GetNeedsSeatingAtStops(),
		NeedsAccessibleToilet:   n.GetNeedsAccessibleToilet(),
		AvoidCobbles:            n.GetAvoidCobbles(),
		LowVision:               n.GetLowVision(),
		Blind:                   n.GetBlind(),
		DeafOrHardOfHearing:     n.GetDeafOrHardOfHearing(),
		SensorySensitive:        n.GetSensorySensitive(),
		PrefersQuietTimes:       n.GetPrefersQuietTimes(),
		NeedsPredictablePlan:    n.GetNeedsPredictablePlan(),
		FatigueOrChronicPain:    n.GetFatigueOrChronicPain(),
		ServiceAnimal:           n.GetServiceAnimal(),
		Stroller:                n.GetStroller(),
		TravellingWithCompanion: n.GetTravellingWithCompanion(),
		Notes:                   n.GetNotes(),
	}
}

// ToProtoAccessibilityNeeds is the inverse of FromProtoAccessibilityNeeds.
// Needs that say nothing are omitted, matching how they are stored.
func ToProtoAccessibilityNeeds(n *locitypes.AccessibilityNeeds) *profilev1.AccessibilityNeeds {
	if !n.Active() {
		return nil
	}
	out := &profilev1.AccessibilityNeeds{
		Mobility:                fromMobility(n.Mobility),
		MaxWalkMinutesPerLeg:    int32(n.MaxWalkMinutesPerLeg),
		MaxWalkMinutesPerDay:    int32(n.MaxWalkMinutesPerDay),
		StepFreeOnly:            n.StepFreeOnly,
		MaxIncline:              fromMaxIncline(n.MaxIncline),
		RestEveryMinutes:        int32(n.RestEveryMinutes),
		NeedsSeatingAtStops:     n.NeedsSeatingAtStops,
		NeedsAccessibleToilet:   n.NeedsAccessibleToilet,
		AvoidCobbles:            n.AvoidCobbles,
		LowVision:               n.LowVision,
		Blind:                   n.Blind,
		DeafOrHardOfHearing:     n.DeafOrHardOfHearing,
		SensorySensitive:        n.SensorySensitive,
		PrefersQuietTimes:       n.PrefersQuietTimes,
		NeedsPredictablePlan:    n.NeedsPredictablePlan,
		FatigueOrChronicPain:    n.FatigueOrChronicPain,
		ServiceAnimal:           n.ServiceAnimal,
		Stroller:                n.Stroller,
		TravellingWithCompanion: n.TravellingWithCompanion,
		Notes:                   n.Notes,
	}
	if n.ConsentAt != nil {
		out.ConsentAt = timestamppb.New(*n.ConsentAt)
	}
	return out
}

func toMobility(v profilev1.MobilityLevel) locitypes.MobilityLevel {
	switch v {
	case profilev1.MobilityLevel_MOBILITY_LEVEL_NONE:
		return locitypes.MobilityNone
	case profilev1.MobilityLevel_MOBILITY_LEVEL_LIMITED_WALKING:
		return locitypes.MobilityLimitedWalking
	case profilev1.MobilityLevel_MOBILITY_LEVEL_WALKING_AID:
		return locitypes.MobilityWalkingAid
	case profilev1.MobilityLevel_MOBILITY_LEVEL_MANUAL_WHEELCHAIR:
		return locitypes.MobilityManualWheelchair
	case profilev1.MobilityLevel_MOBILITY_LEVEL_POWER_WHEELCHAIR:
		return locitypes.MobilityPowerWheelchair
	case profilev1.MobilityLevel_MOBILITY_LEVEL_MOBILITY_SCOOTER:
		return locitypes.MobilityScooter
	default:
		return locitypes.MobilityUnspecified
	}
}

func fromMobility(v locitypes.MobilityLevel) profilev1.MobilityLevel {
	switch v {
	case locitypes.MobilityNone:
		return profilev1.MobilityLevel_MOBILITY_LEVEL_NONE
	case locitypes.MobilityLimitedWalking:
		return profilev1.MobilityLevel_MOBILITY_LEVEL_LIMITED_WALKING
	case locitypes.MobilityWalkingAid:
		return profilev1.MobilityLevel_MOBILITY_LEVEL_WALKING_AID
	case locitypes.MobilityManualWheelchair:
		return profilev1.MobilityLevel_MOBILITY_LEVEL_MANUAL_WHEELCHAIR
	case locitypes.MobilityPowerWheelchair:
		return profilev1.MobilityLevel_MOBILITY_LEVEL_POWER_WHEELCHAIR
	case locitypes.MobilityScooter:
		return profilev1.MobilityLevel_MOBILITY_LEVEL_MOBILITY_SCOOTER
	default:
		return profilev1.MobilityLevel_MOBILITY_LEVEL_UNSPECIFIED
	}
}

func toMaxIncline(v profilev1.MaxIncline) locitypes.MaxIncline {
	switch v {
	case profilev1.MaxIncline_MAX_INCLINE_ANY:
		return locitypes.MaxInclineAny
	case profilev1.MaxIncline_MAX_INCLINE_PCT_3:
		return locitypes.MaxIncline3
	case profilev1.MaxIncline_MAX_INCLINE_PCT_6:
		return locitypes.MaxIncline6
	case profilev1.MaxIncline_MAX_INCLINE_PCT_10:
		return locitypes.MaxIncline10
	default:
		return locitypes.MaxInclineUnspecified
	}
}

func fromMaxIncline(v locitypes.MaxIncline) profilev1.MaxIncline {
	switch v {
	case locitypes.MaxInclineAny:
		return profilev1.MaxIncline_MAX_INCLINE_ANY
	case locitypes.MaxIncline3:
		return profilev1.MaxIncline_MAX_INCLINE_PCT_3
	case locitypes.MaxIncline6:
		return profilev1.MaxIncline_MAX_INCLINE_PCT_6
	case locitypes.MaxIncline10:
		return profilev1.MaxIncline_MAX_INCLINE_PCT_10
	default:
		return profilev1.MaxIncline_MAX_INCLINE_UNSPECIFIED
	}
}
