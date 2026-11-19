package trip

import (
	"context"
	"testing"
	"time"

	poiv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/poi"
	tripv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/placecheck"
)

type fakeStopChecker struct {
	got     []placecheck.Day
	answers [][]placecheck.Check

	moves      []placecheck.Move
	movesAsked []placecheck.Day
}

func (f *fakeStopChecker) Check(_ context.Context, days []placecheck.Day) [][]placecheck.Check {
	f.got = days
	return f.answers
}

func (f *fakeStopChecker) Moves(_ context.Context, days []placecheck.Day) []placecheck.Move {
	f.movesAsked = days
	return f.moves
}

func checkF64(v float64) *float64 { return &v }
func checkI32(v int32) *int32     { return &v }
func checkStr(v string) *string   { return &v }

func TestCheckStopsFillsStopsAndTheDayVerdict(t *testing.T) {
	tuesday := time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)
	checkedAt := tuesday.Add(-time.Hour)
	draft := &tripv1.TripDraft{Days: []*tripv1.TripDay{{
		DayNumber: 1, Date: timestamppb.New(tuesday), CityLat: checkF64(38.72), CityLon: checkF64(-9.14),
		Stops: []*tripv1.TripStop{
			{PoiId: "a", Name: "Lunch", Poi: &poiv1.POIDetailedInfo{Name: "Museu Nacional do Azulejo", Category: "Museum"}, StartMinute: checkI32(570), DurationMinutes: checkI32(90)},
			{PoiId: "b", Name: "Tasca do Chico"},
			{PoiId: "c", Name: "Still being looked up"},
			{Name: "A pasted pin"},
		},
	}}}
	checker := &fakeStopChecker{answers: [][]placecheck.Check{{
		{Status: placecheck.StatusChecked, Hours: placecheck.HoursOpensLater, OpensMinute: 600, ClosesMinute: 1080, Holiday: "Natal", HolidayHoursUnknown: true, CheckedAt: checkedAt},
		{Status: placecheck.StatusNotFound, CheckedAt: checkedAt},
		{},
		{},
	}}}
	h := (&Handler{}).WithStopChecks(checker)

	h.checkStops(context.Background(), draft)

	// What the checker was asked.
	day := checker.got[0]
	if day.Date == nil || !day.Date.Equal(tuesday) || !day.HasCentre || day.Centre.Lat != 38.72 || day.Centre.Lon != -9.14 {
		t.Errorf("day asked = %+v, want Tuesday around the day's city", day)
	}
	if day.Stops[0].Name != "Museu Nacional do Azulejo" {
		t.Errorf("asked for %q, want the place's own name, not the stop's", day.Stops[0].Name)
	}
	if day.Stops[0].Category != "Museum" {
		t.Errorf("category asked = %q, want the place's own", day.Stops[0].Category)
	}
	if day.Stops[1].Name != "Tasca do Chico" || day.Stops[3].POIID != "" {
		t.Errorf("stops asked = %+v", day.Stops)
	}
	if day.Stops[0].StartMinute == nil || *day.Stops[0].StartMinute != 570 {
		t.Errorf("start minute not passed through: %+v", day.Stops[0])
	}

	// What the response carries.
	stops := draft.GetDays()[0].GetStops()
	found := stops[0].GetCheck()
	if found.GetStatus() != tripv1.StopCheckStatus_STOP_CHECK_STATUS_CHECKED ||
		found.GetHours() != tripv1.StopHoursState_STOP_HOURS_STATE_OPENS_LATER ||
		found.GetOpensMinute() != 600 || found.GetClosesMinute() != 1080 ||
		found.GetHolidayName() != "Natal" || !found.GetHolidayHoursUnknown() ||
		found.GetSource() != "osm" || !found.GetCheckedAt().AsTime().Equal(checkedAt) {
		t.Errorf("found stop check = %v", found)
	}
	missing := stops[1].GetCheck()
	if missing.GetStatus() != tripv1.StopCheckStatus_STOP_CHECK_STATUS_NOT_FOUND || missing.OpensMinute != nil {
		t.Errorf("not-found stop check = %v", missing)
	}
	if stops[2].Check != nil || stops[3].Check != nil {
		t.Errorf("a stop with no answer must carry no check: %v, %v", stops[2].Check, stops[3].Check)
	}
	v := draft.GetDays()[0].GetVerdict()
	if v.GetChecked() != 1 || v.GetTotal() != 2 || v.GetNeedAttention() != 1 {
		t.Errorf("verdict = %v, want 1 of 2 checked, 1 needing attention", v)
	}
}

func TestCheckStopsLeavesADayWithNoAnswersWithoutAVerdict(t *testing.T) {
	draft := &tripv1.TripDraft{Days: []*tripv1.TripDay{{Stops: []*tripv1.TripStop{{PoiId: "a", Name: "A"}}}}}
	h := (&Handler{}).WithStopChecks(&fakeStopChecker{answers: [][]placecheck.Check{{{}}}})

	h.checkStops(context.Background(), draft)

	if d := draft.GetDays()[0]; d.Verdict != nil || d.GetStops()[0].Check != nil {
		t.Errorf("day = %v, want no verdict and no check", d)
	}
}

func TestCheckStopsIgnoresAnAnswerOfTheWrongShape(t *testing.T) {
	draft := &tripv1.TripDraft{Days: []*tripv1.TripDay{{Stops: []*tripv1.TripStop{{PoiId: "a", Name: "A"}}}}}
	for name, answers := range map[string][][]placecheck.Check{
		"no days":       nil,
		"too few stops": {{}},
	} {
		h := (&Handler{}).WithStopChecks(&fakeStopChecker{answers: answers})
		h.checkStops(context.Background(), draft)
		if draft.GetDays()[0].GetStops()[0].Check != nil {
			t.Errorf("%s: a check was set from a malformed answer", name)
		}
	}
	// And with no checker at all, nothing happens.
	(&Handler{}).checkStops(context.Background(), draft)
}

func TestDayCentre(t *testing.T) {
	stop := func(id string, lat, lon float64) *tripv1.TripStop {
		return &tripv1.TripStop{PoiId: id, Latitude: checkF64(lat), Longitude: checkF64(lon)}
	}

	got, ok := dayCentre(&tripv1.TripDay{CityLat: checkF64(41.15), CityLon: checkF64(-8.61), Stops: []*tripv1.TripStop{stop("a", 1, 1)}})
	if !ok || got.Lat != 41.15 || got.Lon != -8.61 {
		t.Errorf("with a city: %+v %v, want the city", got, ok)
	}

	// One stop placed in the wrong country must not drag the centre there.
	got, ok = dayCentre(&tripv1.TripDay{Stops: []*tripv1.TripStop{
		stop("a", 38.70, -9.20), stop("b", 38.72, -9.14), stop("c", 48.85, 2.35),
		{Latitude: checkF64(10), Longitude: checkF64(10)}, // a pasted pin: no place behind it
		stop("d", 0, 0), // a place with no stored position
	}})
	if !ok || got.Lat != 38.72 || got.Lon != -9.14 {
		t.Errorf("median of stops: %+v %v, want 38.72,-9.14", got, ok)
	}

	if _, ok = dayCentre(&tripv1.TripDay{Stops: []*tripv1.TripStop{{PoiId: "a"}}}); ok {
		t.Error("a day with no positions has no centre")
	}
}

func TestHoursToProtoCoversEveryState(t *testing.T) {
	seen := map[tripv1.StopHoursState]bool{}
	for h := placecheck.HoursNotListed; h <= placecheck.HoursShutByThen; h++ {
		p := hoursToProto(h)
		if seen[p] {
			t.Errorf("hours %d maps to %v, already used", h, p)
		}
		seen[p] = true
	}
	if len(seen) != len(tripv1.StopHoursState_name) {
		t.Errorf("mapped %d states, the proto has %d", len(seen), len(tripv1.StopHoursState_name))
	}
}

type fakeStopFixer struct {
	trip  *Trip
	moves []StopMove
	done  chan struct{}
}

func (f *fakeStopFixer) OfferMoves(_ context.Context, t *Trip, moves []StopMove) {
	f.trip, f.moves = t, moves
	close(f.done)
}

func TestOfferFixesHandsTheFixerTheMovesByStopAndDayID(t *testing.T) {
	monday := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)
	stop, tue := uuid.New(), uuid.New()
	draft := &tripv1.TripDraft{Days: []*tripv1.TripDay{
		{Id: uuid.NewString(), DayNumber: 1, Date: timestamppb.New(monday), Stops: []*tripv1.TripStop{{Id: stop.String(), PoiId: "a", Name: "Museu"}}},
		{Id: tue.String(), DayNumber: 2, Date: timestamppb.New(monday.AddDate(0, 0, 1)), CityId: checkStr("porto"), TravelDay: true},
		{Id: uuid.NewString(), DayNumber: 3, Date: timestamppb.New(monday.AddDate(0, 0, 2)), CompletedAt: timestamppb.New(monday)},
	}}
	checker := &fakeStopChecker{moves: []placecheck.Move{
		{FromDay: 0, Stop: 0, ToDay: 1, ToIndex: 2, On: placecheck.Check{OpensMinute: 600, ClosesMinute: 1080}},
		{FromDay: 0, Stop: 7, ToDay: 1}, // an index the trip does not have
	}}
	fixer := &fakeStopFixer{done: make(chan struct{})}
	h := (&Handler{}).WithStopChecks(checker).WithStopFixes(fixer)
	owned := &Trip{ID: uuid.New()}

	h.offerFixes(owned, draft)
	select {
	case <-fixer.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the fixer was never told")
	}

	if fixer.trip != owned {
		t.Errorf("fixer got trip %v, want the one read", fixer.trip)
	}
	want := []StopMove{{StopID: stop, ToDayID: tue, ToIndex: 2, Opens: 600, Closes: 1080}}
	if len(fixer.moves) != 1 || fixer.moves[0] != want[0] {
		t.Errorf("moves = %+v, want %+v", fixer.moves, want)
	}
	// What the checker was told about the days: which city, and which are
	// not to be touched.
	if got := checker.movesAsked; len(got) != 3 || got[1].City != "porto" || !got[1].Fixed || !got[2].Fixed || got[0].Fixed {
		t.Errorf("days asked = %+v", got)
	}
}

func TestOfferFixesTellsTheFixerWhenNoMoveHolds(t *testing.T) {
	fixer := &fakeStopFixer{done: make(chan struct{})}
	h := (&Handler{}).WithStopChecks(&fakeStopChecker{}).WithStopFixes(fixer)
	h.offerFixes(&Trip{}, &tripv1.TripDraft{})
	select {
	case <-fixer.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the fixer was never told; a stale offer would stand")
	}
	if len(fixer.moves) != 0 {
		t.Errorf("moves = %+v, want none", fixer.moves)
	}
}

func TestOfferFixesDoesNothingWithoutAFixer(t *testing.T) {
	checker := &fakeStopChecker{}
	(&Handler{}).WithStopChecks(checker).offerFixes(&Trip{}, &tripv1.TripDraft{})
	time.Sleep(20 * time.Millisecond)
	if checker.movesAsked != nil {
		t.Error("asked for moves with nobody to hand them to")
	}
}
