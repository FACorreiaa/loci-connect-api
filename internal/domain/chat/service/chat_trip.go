package service

import (
	recommendationv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/recommendation"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/preference"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// stopsPerDay is the fallback segmentation, used only when nothing told us how
// long the trip is.
//
// It used to be the primary rule, and it had the dependency backwards: days
// were derived from however many places came back, so a four-day trip with
// twenty-four places rendered as six days. Days come from the request now, and
// places are spread across them.
const stopsPerDay = 4

// itineraryPOIs is the list that counts as the plan: the itinerary's own stops,
// or the general list when a turn produced no itinerary.
//
// It is a named function because two callers have to agree on it — the trip
// builder and the paged reader. While the rule lived in one of them, a page of
// "the itinerary" and the trip built from it could disagree about what they
// were describing.
func itineraryPOIs(data *locitypes.AiCityResponse) []locitypes.POIDetailedInfo {
	if data == nil {
		return nil
	}
	if pois := data.AIItineraryResponse.PointsOfInterest; len(pois) > 0 {
		return pois
	}
	return data.PointsOfInterest
}

// normalizeDays makes every place carry a day inside [1, days].
//
// A model asked to number N days will skip one, number from zero, or invent a
// day twelve in a four-day trip. Both clients group on this field, so it has to
// be right before it leaves here rather than defensively handled at each
// renderer. Anything out of range is redistributed evenly, preserving the
// order the model chose — which is also the walking order it was asked for.
func normalizeDays(pois []locitypes.POIDetailedInfo, days int) []locitypes.POIDetailedInfo {
	if len(pois) == 0 {
		return pois
	}
	if days < 1 {
		days = 1
	}

	inRange := 0
	for _, poi := range pois {
		if poi.Day >= 1 && poi.Day <= days {
			inRange++
		}
	}

	// Trust the model only when it numbered essentially the whole list. A
	// half-numbered list is worse than an unnumbered one: the places it did
	// number pin themselves to days the redistribution then has to work
	// around, and the result reads as arbitrary rather than as even.
	if inRange == len(pois) {
		return pois
	}

	return assignDaysByChunk(pois, (len(pois)+days-1)/days, days)
}

// assignDaysByChunk numbers places in runs of perDay, capped at maxDay.
func assignDaysByChunk(pois []locitypes.POIDetailedInfo, perDay, maxDay int) []locitypes.POIDetailedInfo {
	if perDay < 1 {
		perDay = 1
	}
	out := make([]locitypes.POIDetailedInfo, len(pois))
	copy(out, pois)
	for i := range out {
		day := i/perDay + 1
		if maxDay > 0 && day > maxDay {
			day = maxDay
		}
		out[i].Day = day
	}
	return out
}

// buildTripFromCityResponse converts a generated itinerary into an editable
// TripDraft, segmenting the curated POIs into days. Returns nil when there is
// nothing worth persisting. The user then edits/reorders via TripService.
func buildTripFromCityResponse(cc *common.ChatContext, data *locitypes.AiCityResponse, runID string) *trip.Trip {
	if data == nil {
		return nil
	}
	pois := itineraryPOIs(data)
	if len(pois) == 0 {
		return nil
	}

	// Days come from the request when it gave one, and the places are spread
	// over them. With no parsed span — ContinueSessionStreamed, the legacy
	// worker — the old rule stands unchanged: four stops a day, however many
	// days that turns out to be.
	if cc.TripDays >= 1 {
		pois = normalizeDays(pois, cc.TripDays)
	} else {
		pois = assignDaysByChunk(pois, stopsPerDay, 0)
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

	// Indices, not pointers: appending to t.Days reallocates it, and a pointer
	// taken before that append would be writing into the old array.
	dayIndex := map[int]int{}
	for i, poi := range pois {
		idx, ok := dayIndex[poi.Day]
		if !ok {
			t.Days = append(t.Days, trip.TripDay{DayNumber: int32(poi.Day)})
			idx = len(t.Days) - 1
			dayIndex[poi.Day] = idx
		}
		day := &t.Days[idx]
		day.Stops = append(day.Stops, trip.TripStop{
			POIID:      poi.ID.String(),
			OrderIndex: int32(len(day.Stops)),
			Name:       poiName(poi),
			Notes:      stopNotes(poi),
			RecommendationTrace: recommendationTraceForTripStop(
				cc, poi.ID.String(), int32(i), runID,
			),
		})
	}
	return t
}

func recommendationTraceForTripStop(
	cc *common.ChatContext,
	poiID string,
	rank int32,
	runID string,
) *trip.RecommendationTrace {
	if runID == "" || poiID == "" || poiID == "00000000-0000-0000-0000-000000000000" {
		return nil
	}
	return &trip.RecommendationTrace{
		RunID:             runID,
		ItemID:            poiID,
		Rank:              rank,
		AlgorithmVersion:  "itinerary-gemini-v1",
		ExperimentVariant: preference.ExperimentVariant(cc.UserID),
		Surface:           int32(recommendationv1.RecommendationSurface_RECOMMENDATION_SURFACE_TRIP),
		Channel:           int32(recommendationv1.RecommendationChannel_RECOMMENDATION_CHANNEL_WEB),
	}
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
