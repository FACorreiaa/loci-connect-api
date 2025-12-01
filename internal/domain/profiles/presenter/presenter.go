package presenter

import (
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	profilev1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/profile"

	"github.com/FACorreiaa/loci-connect-api/internal/types"
)

func ParseUUID(id string) (uuid.UUID, error) {
	return uuid.Parse(id)
}

func FromCreateProto(req *profilev1.CreateUserPreferenceProfileRequest) (types.CreateUserPreferenceProfileParams, error) {
	params := types.CreateUserPreferenceProfileParams{
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
			return types.CreateUserPreferenceProfileParams{}, err
		}
		params.Tags = tagIDs
	}
	if len(req.InterestIds) > 0 {
		interestIDs, err := parseUUIDList(req.InterestIds)
		if err != nil {
			return types.CreateUserPreferenceProfileParams{}, err
		}
		params.Interests = interestIDs
	}

	return params, nil
}

func FromUpdateProto(req *profilev1.UpdateUserPreferenceProfileRequest) (types.UpdateSearchProfileParams, error) {
	params := types.UpdateSearchProfileParams{}
	if req.ProfileName != nil {
		params.ProfileName = req.GetProfileName()
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
			return types.UpdateSearchProfileParams{}, err
		}
		for _, id := range tagIDs {
			v := id.String()
			params.Tags = append(params.Tags, &v)
		}
	}
	if len(req.InterestIds) > 0 {
		interestIDs, err := parseUUIDList(req.InterestIds)
		if err != nil {
			return types.UpdateSearchProfileParams{}, err
		}
		for _, id := range interestIDs {
			v := id.String()
			params.Interests = append(params.Interests, &v)
		}
	}

	return params, nil
}

func ToProtoProfiles(list []types.UserPreferenceProfileResponse) []*profilev1.UserPreferenceProfile {
	out := make([]*profilev1.UserPreferenceProfile, 0, len(list))
	for _, p := range list {
		out = append(out, ToProtoProfile(p))
	}
	return out
}

func ToProtoProfile(p types.UserPreferenceProfileResponse) *profilev1.UserPreferenceProfile {
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
	return resp
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

func toDayPreference(val profilev1.DayPreference) types.DayPreference {
	switch val {
	case profilev1.DayPreference_DAY_PREFERENCE_DAY:
		return types.DayPreferenceDay
	case profilev1.DayPreference_DAY_PREFERENCE_NIGHT:
		return types.DayPreferenceNight
	default:
		return types.DayPreferenceAny
	}
}

func fromDayPreference(val types.DayPreference) profilev1.DayPreference {
	switch val {
	case types.DayPreferenceDay:
		return profilev1.DayPreference_DAY_PREFERENCE_DAY
	case types.DayPreferenceNight:
		return profilev1.DayPreference_DAY_PREFERENCE_NIGHT
	default:
		return profilev1.DayPreference_DAY_PREFERENCE_ANY
	}
}

func toSearchPace(val profilev1.SearchPace) types.SearchPace {
	switch val {
	case profilev1.SearchPace_SEARCH_PACE_RELAXED:
		return types.SearchPaceRelaxed
	case profilev1.SearchPace_SEARCH_PACE_MODERATE:
		return types.SearchPaceModerate
	case profilev1.SearchPace_SEARCH_PACE_FAST:
		return types.SearchPaceFast
	default:
		return types.SearchPaceAny
	}
}

func fromSearchPace(val types.SearchPace) profilev1.SearchPace {
	switch val {
	case types.SearchPaceRelaxed:
		return profilev1.SearchPace_SEARCH_PACE_RELAXED
	case types.SearchPaceModerate:
		return profilev1.SearchPace_SEARCH_PACE_MODERATE
	case types.SearchPaceFast:
		return profilev1.SearchPace_SEARCH_PACE_FAST
	default:
		return profilev1.SearchPace_SEARCH_PACE_ANY
	}
}

func toTransportPreference(val profilev1.TransportPreference) types.TransportPreference {
	switch val {
	case profilev1.TransportPreference_TRANSPORT_PREFERENCE_WALK:
		return types.TransportPreferenceWalk
	case profilev1.TransportPreference_TRANSPORT_PREFERENCE_PUBLIC:
		return types.TransportPreferencePublic
	case profilev1.TransportPreference_TRANSPORT_PREFERENCE_CAR:
		return types.TransportPreferenceCar
	default:
		return types.TransportPreferenceAny
	}
}

func fromTransportPreference(val types.TransportPreference) profilev1.TransportPreference {
	switch val {
	case types.TransportPreferenceWalk:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_WALK
	case types.TransportPreferencePublic:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_PUBLIC
	case types.TransportPreferenceCar:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_CAR
	default:
		return profilev1.TransportPreference_TRANSPORT_PREFERENCE_ANY
	}
}
