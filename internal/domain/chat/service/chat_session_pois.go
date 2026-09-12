package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// Paging bounds for a stored answer.
//
// The maximum matches PaginationRequest's own lte:100, so a request the proto
// accepts is never silently reshaped here.
const (
	defaultSessionPOIPageSize = 12
	maxSessionPOIPageSize     = 100
)

// GetSessionPOIs reads one page of an answer that has already been generated.
//
// Generation stays a single up-front call; this is delivery. Nothing here
// reaches a model, spends quota, or writes anything — it is a read of
// current_itinerary, which the turn stored on its way out.
//
// Ordering is the stored array's own order, which is the only stable key
// available and also the right one: ungrounded places all carry the nil UUID
// so id cannot order them, and name would reshuffle the moment a later turn
// rewrote the plan. Array position is the order the model was asked to produce,
// which is the walking order the day grouping depends on.
func (l *ServiceImpl) GetSessionPOIs(
	ctx context.Context,
	userID, sessionID uuid.UUID,
	section locitypes.SessionPOISection,
	page, pageSize int,
) (*locitypes.SessionPOIPage, error) {
	session, err := l.llmInteractionRepo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrSessionNotFound, err)
	}
	if session.UserID != userID {
		return nil, common.ErrUnauthorized
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultSessionPOIPageSize
	}
	if pageSize > maxSessionPOIPageSize {
		pageSize = maxSessionPOIPageSize
	}

	all, resolved := sessionPOIsFor(session.CurrentItinerary, section)

	out := &locitypes.SessionPOIPage{
		Section:  resolved,
		Total:    len(all),
		Page:     page,
		PageSize: pageSize,
	}
	if session.CurrentItinerary != nil {
		out.PlannedDays = session.CurrentItinerary.AIItineraryResponse.PlannedDays
	}

	offset := (page - 1) * pageSize
	if offset >= len(all) {
		// Past the end is an empty page, not an error: a client that keeps
		// pressing "more" should stop, not see a failure.
		out.POIs = []locitypes.POIDetailedInfo{}
		return out, nil
	}

	end := min(offset+pageSize, len(all))
	out.POIs = all[offset:end]
	out.HasMore = end < len(all)
	return out, nil
}

// sessionPOIsFor picks the list a section names, and reports which one it
// actually returned.
//
// Hotels and restaurants come back as POIs because that is how the stream
// already delivers them (convertHotelsToPOIs / convertRestaurantsToPOIs): a
// page has to look like the first payload looked, or the client needs two
// renderers for one list.
func sessionPOIsFor(
	data *locitypes.AiCityResponse,
	section locitypes.SessionPOISection,
) ([]locitypes.POIDetailedInfo, locitypes.SessionPOISection) {
	if data == nil {
		return nil, section
	}

	switch section {
	case locitypes.SectionGeneral:
		return data.PointsOfInterest, locitypes.SectionGeneral
	case locitypes.SectionRestaurants:
		return convertRestaurantsToPOIs(data.Restaurants), locitypes.SectionRestaurants
	case locitypes.SectionHotels:
		return convertHotelsToPOIs(data.Hotels), locitypes.SectionHotels
	case locitypes.SectionActivities:
		return data.Activities, locitypes.SectionActivities
	default:
		// itineraryPOIs already falls back to the general list, so report
		// which one the caller is actually looking at rather than claiming an
		// itinerary that does not exist.
		pois := itineraryPOIs(data)
		if len(data.AIItineraryResponse.PointsOfInterest) == 0 && len(pois) > 0 {
			return pois, locitypes.SectionGeneral
		}
		return pois, locitypes.SectionItinerary
	}
}
