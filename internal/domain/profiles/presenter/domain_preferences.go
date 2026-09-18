package presenter

import (
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	interestv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/interest"
	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"
	"google.golang.org/protobuf/types/known/timestamppb"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The four domain-preference blocks travel on the create, update and read
// messages alike. Until these converters existed the presenter simply skipped
// them, which left the repository code that persists them unreachable and gave
// every user the hard-coded defaults seeded by migration 0018.

func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func fromProtoRangeFilter(r *commonpb.RangeFilter) *locitypes.RangeFilter {
	if r == nil {
		return nil
	}
	return &locitypes.RangeFilter{Min: r.Min, Max: r.Max}
}

func toProtoRangeFilter(r *locitypes.RangeFilter) *commonpb.RangeFilter {
	if r == nil {
		return nil
	}
	return &commonpb.RangeFilter{Min: r.Min, Max: r.Max}
}

func fromProtoAccommodationPreferences(p *profilev1.AccommodationPreferences) *locitypes.AccommodationPreferences {
	if p == nil {
		return nil
	}
	return &locitypes.AccommodationPreferences{
		AccommodationType:  p.GetAccommodationType(),
		StarRating:         fromProtoRangeFilter(p.GetStarRating()),
		PriceRangePerNight: fromProtoRangeFilter(p.GetPriceRangePerNight()),
		Amenities:          p.GetAmenities(),
		RoomType:           p.GetRoomType(),
		ChainPreference:    p.GetChainPreference(),
		CancellationPolicy: p.GetCancellationPolicy(),
		BookingFlexibility: p.GetBookingFlexibility(),
	}
}

func ToProtoAccommodationPreferences(p *locitypes.AccommodationPreferences) *profilev1.AccommodationPreferences {
	if p == nil {
		return nil
	}
	return &profilev1.AccommodationPreferences{
		Id:                 p.ID.String(),
		UserPreferenceId:   p.UserPreferenceID.String(),
		AccommodationType:  p.AccommodationType,
		StarRating:         toProtoRangeFilter(p.StarRating),
		PriceRangePerNight: toProtoRangeFilter(p.PriceRangePerNight),
		Amenities:          p.Amenities,
		RoomType:           p.RoomType,
		ChainPreference:    optionalString(p.ChainPreference),
		CancellationPolicy: p.CancellationPolicy,
		BookingFlexibility: optionalString(p.BookingFlexibility),
		CreatedAt:          timestamppb.New(p.CreatedAt),
		UpdatedAt:          timestamppb.New(p.UpdatedAt),
	}
}

func fromProtoDiningPreferences(p *profilev1.DiningPreferences) *locitypes.DiningPreferences {
	if p == nil {
		return nil
	}
	return &locitypes.DiningPreferences{
		CuisineTypes:         p.GetCuisineTypes(),
		MealTypes:            p.GetMealTypes(),
		ServiceStyle:         p.GetServiceStyle(),
		PriceRangePerPerson:  fromProtoRangeFilter(p.GetPriceRangePerPerson()),
		DietaryNeeds:         p.GetDietaryNeeds(),
		AllergenFree:         p.GetAllergenFree(),
		MichelinRated:        p.GetMichelinRated(),
		LocalRecommendations: p.GetLocalRecommendations(),
		ChainVsLocal:         p.GetChainVsLocal(),
		OrganicPreference:    p.GetOrganicPreference(),
		OutdoorSeatingPref:   p.GetOutdoorSeatingPreferred(),
	}
}

func ToProtoDiningPreferences(p *locitypes.DiningPreferences) *profilev1.DiningPreferences {
	if p == nil {
		return nil
	}
	return &profilev1.DiningPreferences{
		Id:                      p.ID.String(),
		UserPreferenceId:        p.UserPreferenceID.String(),
		CuisineTypes:            p.CuisineTypes,
		MealTypes:               p.MealTypes,
		ServiceStyle:            p.ServiceStyle,
		PriceRangePerPerson:     toProtoRangeFilter(p.PriceRangePerPerson),
		DietaryNeeds:            p.DietaryNeeds,
		AllergenFree:            p.AllergenFree,
		MichelinRated:           p.MichelinRated,
		LocalRecommendations:    p.LocalRecommendations,
		ChainVsLocal:            optionalString(p.ChainVsLocal),
		OrganicPreference:       p.OrganicPreference,
		OutdoorSeatingPreferred: p.OutdoorSeatingPref,
		CreatedAt:               timestamppb.New(p.CreatedAt),
		UpdatedAt:               timestamppb.New(p.UpdatedAt),
	}
}

func fromProtoActivityPreferences(p *profilev1.ActivityPreferences) *locitypes.ActivityPreferences {
	if p == nil {
		return nil
	}
	return &locitypes.ActivityPreferences{
		ActivityCategories:     p.GetActivityCategories(),
		PhysicalActivityLevel:  p.GetPhysicalActivityLevel(),
		IndoorOutdoorPref:      p.GetIndoorOutdoorPreference(),
		CulturalImmersionLevel: p.GetCulturalImmersionLevel(),
		MustSeeVsHiddenGems:    p.GetMustSeeVsHiddenGems(),
		EducationalPreference:  p.GetEducationalPreference(),
		PhotoOpportunities:     p.GetPhotographyOpportunities(),
		SeasonSpecific:         p.GetSeasonSpecificActivities(),
		AvoidCrowds:            p.GetAvoidCrowds(),
		LocalEventsInterest:    p.GetLocalEventsInterest(),
	}
}

func ToProtoActivityPreferences(p *locitypes.ActivityPreferences) *profilev1.ActivityPreferences {
	if p == nil {
		return nil
	}
	return &profilev1.ActivityPreferences{
		Id:                       p.ID.String(),
		UserPreferenceId:         p.UserPreferenceID.String(),
		ActivityCategories:       p.ActivityCategories,
		PhysicalActivityLevel:    optionalString(p.PhysicalActivityLevel),
		IndoorOutdoorPreference:  optionalString(p.IndoorOutdoorPref),
		CulturalImmersionLevel:   optionalString(p.CulturalImmersionLevel),
		MustSeeVsHiddenGems:      optionalString(p.MustSeeVsHiddenGems),
		EducationalPreference:    p.EducationalPreference,
		PhotographyOpportunities: p.PhotoOpportunities,
		SeasonSpecificActivities: p.SeasonSpecific,
		AvoidCrowds:              p.AvoidCrowds,
		LocalEventsInterest:      p.LocalEventsInterest,
		CreatedAt:                timestamppb.New(p.CreatedAt),
		UpdatedAt:                timestamppb.New(p.UpdatedAt),
	}
}

func fromProtoItineraryPreferences(p *profilev1.ItineraryPreferences) *locitypes.ItineraryPreferences {
	if p == nil {
		return nil
	}
	return &locitypes.ItineraryPreferences{
		PlanningStyle:         p.GetPlanningStyle(),
		PreferredPace:         p.GetPreferredPace(),
		TimeFlexibility:       p.GetTimeFlexibility(),
		MorningVsEvening:      p.GetMorningVsEvening(),
		WeekendVsWeekday:      p.GetWeekendVsWeekday(),
		PreferredSeasons:      p.GetPreferredSeasons(),
		AvoidPeakSeason:       p.GetAvoidPeakSeason(),
		AdventureVsRelaxation: p.GetAdventureVsRelaxation(),
		SpontaneousVsPlanned:  p.GetSpontaneousVsPlanned(),
	}
}

func ToProtoItineraryPreferences(p *locitypes.ItineraryPreferences) *profilev1.ItineraryPreferences {
	if p == nil {
		return nil
	}
	return &profilev1.ItineraryPreferences{
		Id:                    p.ID.String(),
		UserPreferenceId:      p.UserPreferenceID.String(),
		PlanningStyle:         optionalString(p.PlanningStyle),
		PreferredPace:         optionalString(p.PreferredPace),
		TimeFlexibility:       optionalString(p.TimeFlexibility),
		MorningVsEvening:      optionalString(p.MorningVsEvening),
		WeekendVsWeekday:      optionalString(p.WeekendVsWeekday),
		PreferredSeasons:      p.PreferredSeasons,
		AvoidPeakSeason:       p.AvoidPeakSeason,
		AdventureVsRelaxation: optionalString(p.AdventureVsRelaxation),
		SpontaneousVsPlanned:  optionalString(p.SpontaneousVsPlanned),
		CreatedAt:             timestamppb.New(p.CreatedAt),
		UpdatedAt:             timestamppb.New(p.UpdatedAt),
	}
}

// Interests and tags live in their own tables and are attached to the profile by
// the repository. They were previously dropped on the way out, so the client
// could never render what the user had picked.

func toProtoInterests(list []*locitypes.Interest) []*interestv1.Interest {
	if len(list) == 0 {
		return nil
	}
	out := make([]*interestv1.Interest, 0, len(list))
	for _, i := range list {
		if i == nil {
			continue
		}
		item := &interestv1.Interest{
			Id:          i.ID.String(),
			Name:        i.Name,
			Description: i.Description,
			Active:      i.Active,
			CreatedAt:   timestamppb.New(i.CreatedAt),
			Source:      i.Source,
		}
		if i.UpdatedAt != nil {
			item.UpdatedAt = timestamppb.New(*i.UpdatedAt)
		}
		out = append(out, item)
	}
	return out
}

func toProtoTags(list []*locitypes.Tags) []*interestv1.Tags {
	if len(list) == 0 {
		return nil
	}
	out := make([]*interestv1.Tags, 0, len(list))
	for _, t := range list {
		if t == nil {
			continue
		}
		item := &interestv1.Tags{
			Id:          t.ID.String(),
			Name:        t.Name,
			TagType:     t.TagType,
			Description: t.Description,
			Source:      t.Source,
			Active:      t.Active,
			CreatedAt:   timestamppb.New(t.CreatedAt),
		}
		if t.UpdatedAt != nil {
			item.UpdatedAt = timestamppb.New(*t.UpdatedAt)
		}
		out = append(out, item)
	}
	return out
}
