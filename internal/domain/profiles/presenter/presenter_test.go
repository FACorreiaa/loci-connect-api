package presenter

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"
	profilev1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/profile"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// The four domain-preference blocks are the whole preferences questionnaire.
// They were carried by the proto and dropped on the floor by the presenter, so
// the repository code that persists them was unreachable over RPC.
func TestFromCreateProtoCarriesDomainPreferences(t *testing.T) {
	req := &profilev1.CreateUserPreferenceProfileRequest{
		ProfileName: "Weekend",
		AccommodationPreferences: &profilev1.AccommodationPreferences{
			AccommodationType: []string{"hostel"},
			StarRating:        &commonpb.RangeFilter{Min: proto.Float64(2), Max: proto.Float64(4)},
			ChainPreference:   proto.String("independent"),
		},
		DiningPreferences: &profilev1.DiningPreferences{
			CuisineTypes:  []string{"portuguese"},
			MichelinRated: true,
		},
		ActivityPreferences: &profilev1.ActivityPreferences{
			ActivityCategories: []string{"nature"},
			AvoidCrowds:        true,
		},
		ItineraryPreferences: &profilev1.ItineraryPreferences{
			PlanningStyle:    proto.String("spontaneous"),
			PreferredSeasons: []string{"winter"},
		},
	}

	params, err := FromCreateProto(req)
	if err != nil {
		t.Fatalf("FromCreateProto: %v", err)
	}

	if params.AccommodationPreferences == nil {
		t.Fatal("accommodation preferences dropped")
	}
	if got := params.AccommodationPreferences.AccommodationType; len(got) != 1 || got[0] != "hostel" {
		t.Errorf("accommodation_type = %v", got)
	}
	if r := params.AccommodationPreferences.StarRating; r == nil || r.Min == nil || *r.Min != 2 {
		t.Errorf("star_rating = %+v", r)
	}
	if params.AccommodationPreferences.ChainPreference != "independent" {
		t.Errorf("chain_preference = %q", params.AccommodationPreferences.ChainPreference)
	}
	if params.DiningPreferences == nil || !params.DiningPreferences.MichelinRated {
		t.Errorf("dining preferences dropped: %+v", params.DiningPreferences)
	}
	if params.ActivityPreferences == nil || !params.ActivityPreferences.AvoidCrowds {
		t.Errorf("activity preferences dropped: %+v", params.ActivityPreferences)
	}
	if params.ItineraryPreferences == nil || params.ItineraryPreferences.PlanningStyle != "spontaneous" {
		t.Errorf("itinerary preferences dropped: %+v", params.ItineraryPreferences)
	}
}

// The proto's own rule allows 0..10 while the column CHECK is 0..4, so an
// out-of-range value used to pass validation and then fail at the database as an
// opaque CodeInternal.
func TestBudgetLevelIsRejectedOutsideColumnRange(t *testing.T) {
	if _, err := FromCreateProto(&profilev1.CreateUserPreferenceProfileRequest{
		ProfileName: "Lavish",
		BudgetLevel: proto.Int32(7),
	}); err == nil {
		t.Error("create: expected budget_level 7 to be rejected")
	}

	if _, err := FromUpdateProto(&profilev1.UpdateUserPreferenceProfileRequest{
		ProfileId:   uuid.New().String(),
		BudgetLevel: proto.Int32(7),
	}); err == nil {
		t.Error("update: expected budget_level 7 to be rejected")
	}

	if _, err := FromCreateProto(&profilev1.CreateUserPreferenceProfileRequest{
		ProfileName: "Thrifty",
		BudgetLevel: proto.Int32(4),
	}); err != nil {
		t.Errorf("create: budget_level 4 should be accepted, got %v", err)
	}
}

// Interest and tag ids are UUIDs. The client used to send display names here,
// which is what made every create with a selection fail outright.
func TestNonUUIDInterestIDIsRejected(t *testing.T) {
	_, err := FromCreateProto(&profilev1.CreateUserPreferenceProfileRequest{
		ProfileName: "Cultural",
		InterestIds: []string{"Art & Culture"},
	})
	if err == nil {
		t.Fatal("expected a display name in interest_ids to be rejected")
	}
	if !strings.Contains(err.Error(), "Art & Culture") {
		t.Errorf("error should name the offending value, got %v", err)
	}
}

// An update replaces the list-valued fields, so the repository's "supplied?"
// check fires and a user who deselects their last vibe actually clears it.
func TestFromUpdateProtoAlwaysSuppliesLists(t *testing.T) {
	params, err := FromUpdateProto(&profilev1.UpdateUserPreferenceProfileRequest{
		ProfileId: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("FromUpdateProto: %v", err)
	}
	if params.PreferredVibes == nil {
		t.Error("preferred_vibes should be an empty slice, not nil")
	}
	if params.DietaryNeeds == nil {
		t.Error("dietary_needs should be an empty slice, not nil")
	}
	if params.Interests == nil {
		t.Error("interests should be an empty slice, not nil")
	}
	if params.Tags == nil {
		t.Error("tags should be an empty slice, not nil")
	}
	if params.ProfileName != nil {
		t.Error("an absent profile_name should stay nil")
	}
}

// ToProtoProfile emitted neither the interests and tags the user picked nor the
// four domain blocks, so every editor it fed fell back to hard-coded defaults.
func TestToProtoProfileEmitsAssociationsAndDomainPreferences(t *testing.T) {
	active := true
	p := locitypes.UserPreferenceProfileResponse{
		ID:          uuid.New(),
		UserID:      uuid.New(),
		ProfileName: "Weekend",
		Interests: []*locitypes.Interest{
			{ID: uuid.New(), Name: "History", Active: &active, CreatedAt: time.Now()},
		},
		Tags: []*locitypes.Tags{
			{ID: uuid.New(), Name: "Crowded", TagType: "personal", CreatedAt: time.Now()},
		},
		AccommodationPreferences: &locitypes.AccommodationPreferences{
			AccommodationType: []string{"hostel"},
			StarRating:        &locitypes.RangeFilter{Min: proto.Float64(2)},
		},
		DiningPreferences:    &locitypes.DiningPreferences{CuisineTypes: []string{"portuguese"}},
		ActivityPreferences:  &locitypes.ActivityPreferences{ActivityCategories: []string{"nature"}},
		ItineraryPreferences: &locitypes.ItineraryPreferences{PlanningStyle: "flexible"},
	}

	out := ToProtoProfile(p)

	if len(out.GetInterests()) != 1 || out.GetInterests()[0].GetName() != "History" {
		t.Errorf("interests = %+v", out.GetInterests())
	}
	if len(out.GetTags()) != 1 || out.GetTags()[0].GetName() != "Crowded" {
		t.Errorf("tags = %+v", out.GetTags())
	}
	if a := out.GetAccommodationPreferences(); a == nil || len(a.GetAccommodationType()) != 1 {
		t.Errorf("accommodation preferences = %+v", a)
	} else if a.GetStarRating().GetMin() != 2 {
		t.Errorf("star_rating min = %v", a.GetStarRating().GetMin())
	}
	if out.GetDiningPreferences() == nil {
		t.Error("dining preferences missing")
	}
	if out.GetActivityPreferences() == nil {
		t.Error("activity preferences missing")
	}
	if out.GetItineraryPreferences().GetPlanningStyle() != "flexible" {
		t.Errorf("itinerary planning_style = %q", out.GetItineraryPreferences().GetPlanningStyle())
	}
}
