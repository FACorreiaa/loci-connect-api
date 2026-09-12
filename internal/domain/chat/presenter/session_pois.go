package presenter

import (
	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"
	commonpb "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/common"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func sectionToProto(section locitypes.SessionPOISection) chatv1.SessionPOISection {
	switch section {
	case locitypes.SectionGeneral:
		return chatv1.SessionPOISection_SESSION_POI_SECTION_GENERAL
	case locitypes.SectionRestaurants:
		return chatv1.SessionPOISection_SESSION_POI_SECTION_RESTAURANTS
	case locitypes.SectionHotels:
		return chatv1.SessionPOISection_SESSION_POI_SECTION_HOTELS
	case locitypes.SectionActivities:
		return chatv1.SessionPOISection_SESSION_POI_SECTION_ACTIVITIES
	default:
		return chatv1.SessionPOISection_SESSION_POI_SECTION_ITINERARY
	}
}

// ToGetSessionPOIsResponse renders one page of a stored answer.
//
// has_more is carried from the page rather than recomputed: the reader already
// knows whether it stopped short of the end, and a "load more" button is wired
// straight to this field.
func ToGetSessionPOIsResponse(page *locitypes.SessionPOIPage) *chatv1.GetSessionPOIsResponse {
	if page == nil {
		return &chatv1.GetSessionPOIsResponse{}
	}

	totalPages := int32(0)
	if page.PageSize > 0 {
		totalPages = int32((page.Total + page.PageSize - 1) / page.PageSize)
	}

	return &chatv1.GetSessionPOIsResponse{
		PointsOfInterest: ToPOIDetailedInfoSlice(page.POIs),
		Pagination: &commonpb.PaginationMetadata{
			TotalRecords: int32(page.Total),
			Page:         int32(page.Page),
			PageSize:     int32(page.PageSize),
			TotalPages:   totalPages,
			HasMore:      page.HasMore,
		},
		Section:     sectionToProto(page.Section),
		PlannedDays: int32(page.PlannedDays),
	}
}
