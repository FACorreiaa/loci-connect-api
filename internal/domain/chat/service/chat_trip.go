package service

import (
	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// stopsPerDay is the default day segmentation for an auto-generated trip.
const stopsPerDay = 4

// buildTripFromCityResponse converts a generated itinerary into an editable
// TripDraft, segmenting the curated POIs into days. Returns nil when there is
// nothing worth persisting. The user then edits/reorders via TripService.
func buildTripFromCityResponse(cc *common.ChatContext, data *locitypes.AiCityResponse) *trip.Trip {
	if data == nil {
		return nil
	}
	pois := data.AIItineraryResponse.PointsOfInterest
	if len(pois) == 0 {
		pois = data.PointsOfInterest
	}
	if len(pois) == 0 {
		return nil
	}

	title := "Trip"
	if cc.CityName != "" {
		title = "Trip to " + cc.CityName
	}
	sessionID := cc.SessionID.String()

	t := &trip.Trip{
		UserID:          cc.UserID,
		CityName:        cc.CityName,
		Title:           title,
		SourceSessionID: &sessionID,
	}

	var day *trip.TripDay
	for i, poi := range pois {
		if i%stopsPerDay == 0 {
			t.Days = append(t.Days, trip.TripDay{DayNumber: int32(len(t.Days) + 1)})
			day = &t.Days[len(t.Days)-1]
		}
		day.Stops = append(day.Stops, trip.TripStop{
			POIID:      poi.ID.String(),
			OrderIndex: int32(i % stopsPerDay),
			Name:       poiName(poi),
			Notes:      stopNotes(poi),
		})
	}
	return t
}

func poiName(p locitypes.POIDetailedInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return "Stop"
}

// stopNotes prefers the short TrustSignals "why this" rationale so the trip
// editor can show a Why-this-stop chip without an extra POI fetch.
func stopNotes(p locitypes.POIDetailedInfo) string {
	_, _, rationale := locitypes.TrustSignals(p)
	if rationale != "" {
		return rationale
	}
	return p.DescriptionPOI
}
