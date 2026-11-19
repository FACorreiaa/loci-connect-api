package trip

import (
	"context"
	"sort"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/placecheck"
	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

// StopChecker says what a real map knows about the stops of a plan:
// out[i][j] answers for day i's stop j. *placecheck.Planner is the one in
// production. It must not block on the map; a trip read waits for it.
type StopChecker interface {
	Check(ctx context.Context, days []placecheck.Day) [][]placecheck.Check
}

// WithStopChecks attaches the checker. Nil is supported and is the default:
// stops then carry no check and clients show no stamp.
func (h *Handler) WithStopChecks(checks StopChecker) *Handler {
	h.checks = checks
	return h
}

// checkStops sets `check` on every stop the map has answered for, and
// `verdict` on every day with at least one. It runs after hydrateStops so it
// can read each place's own name and position.
//
// Nothing here is stored. The answer is worked out from the stop as it is in
// this response, so moving a stop to another day or hour changes its check on
// the next read with nothing to invalidate.
func (h *Handler) checkStops(ctx context.Context, draft *tripv1.TripDraft) {
	if h.checks == nil || draft == nil {
		return
	}
	days := draft.GetDays()
	plan := make([]placecheck.Day, len(days))
	for i, day := range days {
		d := placecheck.Day{Stops: make([]placecheck.Stop, len(day.GetStops()))}
		if day.GetDate() != nil {
			date := day.GetDate().AsTime().UTC()
			d.Date = &date
		}
		d.Centre, d.HasCentre = dayCentre(day)
		for j, stop := range day.GetStops() {
			// The place's own name is what the map is asked for. A stop's
			// name is the traveller's to change ("Lunch") and may no longer
			// name the place.
			name := stop.GetPoi().GetName()
			if name == "" {
				name = stop.GetName()
			}
			d.Stops[j] = placecheck.Stop{
				POIID: stop.GetPoiId(), Name: name, Category: stop.GetPoi().GetCategory(),
				StartMinute: stop.StartMinute, DurationMinutes: stop.DurationMinutes,
			}
		}
		plan[i] = d
	}

	answers := h.checks.Check(ctx, plan)
	if len(answers) != len(days) {
		return
	}
	for i, day := range days {
		stops := day.GetStops()
		if len(answers[i]) != len(stops) {
			continue
		}
		var verdict placecheck.Verdict
		for j, stop := range stops {
			c := answers[i][j]
			if c.Status == placecheck.StatusUnchecked {
				continue
			}
			verdict.Tally(c)
			stop.Check = stopCheckToProto(c)
		}
		if verdict.Total > 0 {
			day.Verdict = &tripv1.DayVerdict{
				//nolint:gosec // a day's stops, far below int32
				Checked: int32(verdict.Checked), Total: int32(verdict.Total), NeedAttention: int32(verdict.NeedAttention),
			}
		}
	}
}

// dayCentre is where to search around for a day's places: the day's city when
// the trip records it, otherwise the middle of the day's own stops. A model's
// position for one place can be kilometres off, but the median of a day's
// worth sits well inside the city the search covers.
func dayCentre(day *tripv1.TripDay) (transit.LatLon, bool) {
	if day.CityLat != nil && day.CityLon != nil && (day.GetCityLat() != 0 || day.GetCityLon() != 0) {
		return transit.LatLon{Lat: day.GetCityLat(), Lon: day.GetCityLon()}, true
	}
	var lats, lons []float64
	for _, stop := range day.GetStops() {
		if stop.GetPoiId() == "" || stop.Latitude == nil || stop.Longitude == nil {
			continue
		}
		if stop.GetLatitude() == 0 && stop.GetLongitude() == 0 {
			continue
		}
		lats = append(lats, stop.GetLatitude())
		lons = append(lons, stop.GetLongitude())
	}
	if len(lats) == 0 {
		return transit.LatLon{}, false
	}
	return transit.LatLon{Lat: median(lats), Lon: median(lons)}, true
}

func median(v []float64) float64 {
	sort.Float64s(v)
	mid := len(v) / 2
	if len(v)%2 == 1 {
		return v[mid]
	}
	return (v[mid-1] + v[mid]) / 2
}

func stopCheckToProto(c placecheck.Check) *tripv1.StopCheck {
	out := &tripv1.StopCheck{Source: placecheck.SourceOSM}
	if !c.CheckedAt.IsZero() {
		out.CheckedAt = timestamppb.New(c.CheckedAt)
	}
	if c.Status == placecheck.StatusNotFound {
		out.Status = tripv1.StopCheckStatus_STOP_CHECK_STATUS_NOT_FOUND
		return out
	}
	out.Status = tripv1.StopCheckStatus_STOP_CHECK_STATUS_CHECKED
	out.Hours = hoursToProto(c.Hours)
	out.HolidayName = c.Holiday
	out.HolidayHoursUnknown = c.HolidayHoursUnknown
	if c.OpensMinute != 0 || c.ClosesMinute != 0 {
		//nolint:gosec // minutes of a day, at most 2880
		opens, closes := int32(c.OpensMinute), int32(c.ClosesMinute)
		out.OpensMinute, out.ClosesMinute = &opens, &closes
	}
	return out
}

func hoursToProto(h placecheck.Hours) tripv1.StopHoursState {
	switch h {
	case placecheck.HoursUnreadable:
		return tripv1.StopHoursState_STOP_HOURS_STATE_UNREADABLE
	case placecheck.HoursListed:
		return tripv1.StopHoursState_STOP_HOURS_STATE_LISTED
	case placecheck.HoursOpenThatDay:
		return tripv1.StopHoursState_STOP_HOURS_STATE_OPEN_THAT_DAY
	case placecheck.HoursOpen:
		return tripv1.StopHoursState_STOP_HOURS_STATE_OPEN
	case placecheck.HoursShutThatDay:
		return tripv1.StopHoursState_STOP_HOURS_STATE_SHUT_THAT_DAY
	case placecheck.HoursOpensLater:
		return tripv1.StopHoursState_STOP_HOURS_STATE_OPENS_LATER
	case placecheck.HoursClosesEarly:
		return tripv1.StopHoursState_STOP_HOURS_STATE_CLOSES_EARLY
	case placecheck.HoursShutByThen:
		return tripv1.StopHoursState_STOP_HOURS_STATE_SHUT_BY_THEN
	default:
		return tripv1.StopHoursState_STOP_HOURS_STATE_UNSPECIFIED
	}
}
