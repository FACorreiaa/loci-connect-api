package presenter

import (
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
)

func ParseUUID(id string) (uuid.UUID, error) {
	return uuid.Parse(id)
}

func FromCreateProto(req *profilev1.CreateUserPreferenceProfileRequest) (locitypes.CreateUserPreferenceProfileParams, error) {
	params := locitypes.CreateUserPreferenceProfileParams{
		ProfileName: req.GetProfileName(),
		PreferredVibes: func() []string {
			if req.PreferredVibes != nil {
				return req.GetPreferredVibes()
			}
			return nil
		}(),
		DietaryNeeds: req.GetDietaryNeeds(),
	}
	if req.IsDefault != nil {
		params.IsDefault = proto.Bool(req.GetIsDefault())
	}
	if req.SearchRadiusKm != nil {
		params.SearchRadiusKm = proto.Float64(req.GetSearchRadiusKm())
	}
	if req.PreferredTime != nil {
		v := toDayPreference(req.GetPreferredTime())
		params.PreferredTime = &v
	}
	if req.BudgetLevel != nil {
		val := int(req.GetBudgetLevel())
		if err := validateBudgetLevel(val); err != nil {
			return locitypes.CreateUserPreferenceProfileParams{}, err
		}
		params.BudgetLevel = &val
	}
	if req.PreferredPace != nil {
		v := toSearchPace(req.GetPreferredPace())
		params.PreferredPace = &v
	}
	if req.PreferAccessiblePois != nil {
		params.PreferAccessiblePOIs = proto.Bool(req.GetPreferAccessiblePois())
	}
	if req.PreferOutdoorSeating != nil {
		params.PreferOutdoorSeating = proto.Bool(req.GetPreferOutdoorSeating())
	}
	if req.PreferDogFriendly != nil {
		params.PreferDogFriendly = proto.Bool(req.GetPreferDogFriendly())
	}
	if req.PreferredTransport != nil {
		v := toTransportPreference(req.GetPreferredTransport())
		params.PreferredTransport = &v
	}

	if len(req.TagIds) > 0 {
		tagIDs, err := parseUUIDList(req.TagIds)
		if err != nil {
			return locitypes.CreateUserPreferenceProfileParams{}, err
		}
		params.Tags = tagIDs
	}
	if len(req.InterestIds) > 0 {
		interestIDs, err := parseUUIDList(req.InterestIds)
		if err != nil {
			return locitypes.CreateUserPreferenceProfileParams{}, err
		}
		params.Interests = interestIDs
	}

	params.AccommodationPreferences = fromProtoAccommodationPreferences(req.GetAccommodationPreferences())
	params.DiningPreferences = fromProtoDiningPreferences(req.GetDiningPreferences())
	params.ActivityPreferences = fromProtoActivityPreferences(req.GetActivityPreferences())
	params.ItineraryPreferences = fromProtoItineraryPreferences(req.GetItineraryPreferences())

	return params, nil
}

func FromUpdateProto(req *profilev1.UpdateUserPreferenceProfileRequest) (locitypes.UpdateSearchProfileParams, error) {
	params := locitypes.UpdateSearchProfileParams{}
	if req.ProfileName != nil {
		params.ProfileName = proto.String(req.GetProfileName())
	}
	if req.IsDefault != nil {
		params.IsDefault = proto.Bool(req.GetIsDefault())
	}
	if req.SearchRadiusKm != nil {
		params.SearchRadiusKm = proto.Float64(req.GetSearchRadiusKm())
	}
	if req.PreferredTime != nil {
		v := toDayPreference(req.GetPreferredTime())
		params.PreferredTime = &v
	}
	if req.BudgetLevel != nil {
		val := int(req.GetBudgetLevel())
		if err := validateBudgetLevel(val); err != nil {
			return locitypes.UpdateSearchProfileParams{}, err
		}
		params.BudgetLevel = &val
	}
	if req.PreferredPace != nil {
		v := toSearchPace(req.GetPreferredPace())
		params.PreferredPace = &v
	}
	if req.PreferAccessiblePois != nil {
		params.PreferAccessiblePOIs = proto.Bool(req.GetPreferAccessiblePois())
	}
	if req.PreferOutdoorSeating != nil {
		params.PreferOutdoorSeating = proto.Bool(req.GetPreferOutdoorSeating())
	}
	if req.PreferDogFriendly != nil {
		params.PreferDogFriendly = proto.Bool(req.GetPreferDogFriendly())
	}
	if req.PreferredTransport != nil {
		v := toTransportPreference(req.GetPreferredTransport())
		params.PreferredTransport = &v
	}

	// An update replaces the list-valued fields wholesale rather than merging
	// them. A proto3 `repeated` field has no presence, so an omitted list and a
	// list the user emptied arrive identically as nil -- with merge semantics,
	// deselecting your last interest or vibe would silently do nothing. The
	// price is that a partial caller must send the lists it wants to keep;
	// useUpdateSettingsMutation on the client merges against the cached profile
	// for exactly that reason.
	tagIDs, err := parseUUIDList(req.TagIds)
	if err != nil {
		return locitypes.UpdateSearchProfileParams{}, err
	}
	params.Tags = tagIDs

	interestIDs, err := parseUUIDList(req.InterestIds)
	if err != nil {
		return locitypes.UpdateSearchProfileParams{}, err
	}
	params.Interests = interestIDs

	params.PreferredVibes = orEmpty(req.GetPreferredVibes())
	params.DietaryNeeds = orEmpty(req.GetDietaryNeeds())

	params.AccommodationPreferences = fromProtoAccommodationPreferences(req.GetAccommodationPreferences())
	params.DiningPreferences = fromProtoDiningPreferences(req.GetDiningPreferences())
	params.ActivityPreferences = fromProtoActivityPreferences(req.GetActivityPreferences())
	params.ItineraryPreferences = fromProtoItineraryPreferences(req.GetItineraryPreferences())

	return params, nil
}

func ToProtoProfiles(list []locitypes.UserPreferenceProfileResponse) []*profilev1.UserPreferenceProfile {
	out := make([]*profilev1.UserPreferenceProfile, 0, len(list))
	for _, p := range list {
		out = append(out, ToProtoProfile(p))
	}
	return out
}

func ToProtoProfile(p locitypes.UserPreferenceProfileResponse) *profilev1.UserPreferenceProfile {
	resp := &profilev1.UserPreferenceProfile{
		Id:                   p.ID.String(),
		UserId:               p.UserID.String(),
		ProfileName:          p.ProfileName,
		IsDefault:            p.IsDefault,
		SearchRadiusKm:       p.SearchRadiusKm,
		PreferredTime:        fromDayPreference(p.PreferredTime),
		BudgetLevel:          int32(p.BudgetLevel),
		PreferredPace:        fromSearchPace(p.PreferredPace),
		PreferAccessiblePois: p.PreferAccessiblePOIs,
		PreferOutdoorSeating: p.PreferOutdoorSeating,
		PreferDogFriendly:    p.PreferDogFriendly,
		PreferredVibes:       p.PreferredVibes,
		PreferredTransport:   fromTransportPreference(p.PreferredTransport),
		DietaryNeeds:         p.DietaryNeeds,
		CreatedAt:            timestamppb.New(p.CreatedAt),
		UpdatedAt:            timestamppb.New(p.UpdatedAt),
	}
	if p.UserLatitude != nil {
		resp.UserLatitude = p.UserLatitude
	}
	if p.UserLongitude != nil {
		resp.UserLongitude = p.UserLongitude
	}
	resp.Interests = toProtoInterests(p.Interests)
	resp.Tags = toProtoTags(p.Tags)
	resp.AccommodationPreferences = ToProtoAccommodationPreferences(p.AccommodationPreferences)
	resp.DiningPreferences = ToProtoDiningPreferences(p.DiningPreferences)
	resp.ActivityPreferences = ToProtoActivityPreferences(p.ActivityPreferences)
	resp.ItineraryPreferences = ToProtoItineraryPreferences(p.ItineraryPreferences)
	return resp
}

// validateBudgetLevel mirrors the CHECK on user_preference_profiles.budget_level
// (migration 0008). The proto rule still allows 0..10, so without this guard a
// value of 5-10 passes validation and then fails at the database as an opaque
// CodeInternal. Narrow the proto rule on the next loci-connect-proto release and
// this becomes belt-and-braces.
func validateBudgetLevel(v int) error {
	if v < 0 || v > 4 {
		return fmt.Errorf("budget_level must be between 0 and 4, got %d", v)
	}
	return nil
}

// orEmpty normalises a nil slice to an empty one, so the repository's
// "supplied?" check fires and the column is written.
func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func parseUUIDList(ids []string) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("invalid id %s: %w", id, err)
		}
		result = append(result, parsed)
	}
	return result, nil
}

func toDayPreference(val profilev1.DayPreference) locitypes.DayPreference {
	switch val {
	case profilev1.DayPreference_DAY_PREFERENCE_DAY:
		return locitypes.DayPreferenceDay
	case profilev1.DayPreference_DAY_PREFERENCE_NIGHT:
		return locitypes.DayPreferenceNight
	default:
		return locitypes.DayPreferenceAny
	}
}

func fromDayPreference(val locitypes.DayPreference) profilev1.DayPreference {
	switch val {
	case locitypes.DayPreferenceDay:
		return profilev1.DayPreference_DAY_PREFERENCE_DAY
	case locitypes.DayPreferenceNight:
		return profilev1.DayPreference_DAY_PREFERENCE_NIGHT
	default:
		return profilev1.DayPreference_DAY_PREFERENCE_ANY
	}
}

func toSearchPace(val profilev1.SearchPace) locitypes.SearchPace {
	switch val {
	case profilev1.SearchPace_SEARCH_PACE_RELAXED:
		return locitypes.SearchPaceRelaxed
	case profilev1.SearchPace_SEARCH_PACE_MODERATE:
		return locitypes.SearchPaceModerate
	case profilev1.SearchPace_SEARCH_PACE_FAST:
		return locitypes.SearchPaceFast
	default:
		return locitypes.SearchPaceAny
	}
}

func fromSearchPace(val locitypes.SearchPace) profilev1.SearchPace {
	switch val {
	case locitypes.SearchPaceRelaxed:
		return profilev1.SearchPace_SEARCH_PACE_RELAXED
	case locitypes.SearchPaceModerate:
		return profilev1.SearchPace_SEARCH_PACE_MODERATE
	case locitypes.SearchPaceFast:
		return profilev1.SearchPace_SEARCH_PACE_FAST
	default:
		return profilev1.SearchPace_SEARCH_PACE_ANY
	}
}

func toTransportPreference(val profilev1.TransportPreference) locitypes.TransportPreference {
	switch val {
	case profilev1.TransportPreference_TRANSPORT_PREFERENCE_WALK:
		return locitypes.TransportPreferenceWalk
	case profilev1.TransportPreference_TRANSPORT_PREFERENCE_PUBLIC:
		return locitypes.TransportPreferencePublic
	case profilev1.TransportPreference_TRANSPORT_PREFERENCE_CAR:
		return locitypes.TransportPreferenceCar
	default:
		return locitypes.TransportPreferenceAny
	}
}

func fromTransportPreference(val locitypes.TransportPreference) profilev1.TransportPreference {
	switch val {
	case locitypes.TransportPreferenceWalk:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_WALK
	case locitypes.TransportPreferencePublic:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_PUBLIC
	case locitypes.TransportPreferenceCar:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_CAR
	default:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_ANY
	}
}
