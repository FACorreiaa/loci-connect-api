package placecheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/transit"
)

type fakeCalendar struct {
	name  string
	asked int
}

func (f *fakeCalendar) Holiday(context.Context, float64, float64, time.Time) string {
	f.asked++
	return f.name
}

func newPlanner(store *fakeStore, finder *fakeFinder, cal Calendar) *Planner {
	return NewPlanner(newService(store, finder), cal, nil)
}

func TestPlannerAnswersFromWhatIsHeld(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{
		"museum": *matched("Tu-Su 10:00-18:00"),
		"gone":   {POIID: "gone", ExpiresAt: tuesday.Add(time.Hour)},
	}}
	finder := &fakeFinder{}
	p := newPlanner(store, finder, nil)

	got := p.Check(context.Background(), []Day{{
		Date: &tuesday, Centre: lisbon, HasCentre: true,
		Stops: []Stop{
			{POIID: "museum", Name: "Museu", StartMinute: ptr(int32(9 * 60)), DurationMinutes: ptr(int32(60))},
			{Name: "A pasted pin"},
			{POIID: "gone", Name: "Gone"},
		},
	}})
	p.Wait()

	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("shape = %v, want one day of three stops", got)
	}
	if c := got[0][0]; c.Status != StatusChecked || c.Hours != HoursOpensLater || c.OpensMinute != 600 {
		t.Errorf("museum = %+v, want checked, opens later at 600", c)
	}
	if c := got[0][1]; c != (Check{}) {
		t.Errorf("stop with no place = %+v, want the zero check", c)
	}
	if c := got[0][2]; c.Status != StatusNotFound {
		t.Errorf("unmatched place = %+v, want not found", c)
	}
	if len(finder.asked) != 0 {
		t.Errorf("map asked %v, want nothing: every place is held and fresh", finder.asked)
	}
}

func TestPlannerLooksUpMissingPlacesBehindTheRead(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{}}
	finder := &fakeFinder{answers: map[string]transit.PlaceMatch{
		"Torre de Belém": {Found: true, Place: transit.MapPlace{
			OSMType: "way", OSMID: 7, Name: "Torre de Belém", Lat: 38.6916, Lon: -9.2160, Confidence: 1,
		}},
	}}
	p := newPlanner(store, finder, nil)
	day := []Day{{Centre: lisbon, HasCentre: true, Stops: []Stop{{POIID: "tower", Name: "Torre de Belém"}}}}

	first := p.Check(context.Background(), day)
	if first[0][0].Status != StatusUnchecked {
		t.Fatalf("first read = %+v, want unchecked: the reader does not wait for the map", first[0][0])
	}
	p.Wait()

	second := p.Check(context.Background(), day)
	p.Wait()
	if second[0][0].Status != StatusChecked {
		t.Errorf("second read = %+v, want checked", second[0][0])
	}
	if len(finder.asked) != 1 {
		t.Errorf("map asked %d times, want once", len(finder.asked))
	}
}

func TestPlannerShowsAStaleAnswerWhileItRefreshes(t *testing.T) {
	stale := *matched("")
	stale.ExpiresAt = tuesday.Add(-time.Hour)
	store := &fakeStore{rows: map[string]Verification{"p1": stale}}
	finder := &fakeFinder{answers: map[string]transit.PlaceMatch{}}
	p := newPlanner(store, finder, nil)

	got := p.Check(context.Background(), []Day{{
		Centre: lisbon, HasCentre: true, Stops: []Stop{{POIID: "p1", Name: "Museu"}},
	}})
	p.Wait()

	if got[0][0].Status != StatusChecked {
		t.Errorf("stale place = %+v, want it still shown as checked", got[0][0])
	}
	if len(finder.asked) != 1 {
		t.Errorf("map asked %d times, want once to refresh", len(finder.asked))
	}
}

func TestPlannerNeverSearchesWithoutACentre(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{}}
	finder := &fakeFinder{}
	p := newPlanner(store, finder, nil)

	got := p.Check(context.Background(), []Day{{Stops: []Stop{{POIID: "p1", Name: "Museu"}}}})
	p.Wait()

	if got[0][0].Status != StatusUnchecked {
		t.Errorf("got %+v, want unchecked", got[0][0])
	}
	if len(finder.asked) != 0 {
		t.Errorf("map asked %v, want nothing: there is nowhere to search around", finder.asked)
	}
}

func TestPlannerPassesTheHolidayToTheHours(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{"p1": *matched("Mo-Su 10:00-18:00")}}
	cal := &fakeCalendar{name: "Dia da Liberdade"}
	p := newPlanner(store, &fakeFinder{}, cal)

	got := p.Check(context.Background(), []Day{
		{Date: &tuesday, Centre: lisbon, HasCentre: true, Stops: []Stop{
			{POIID: "p1", Name: "Museu", StartMinute: ptr(int32(11 * 60)), DurationMinutes: ptr(int32(60))},
		}},
		// No date: there is no day to be a holiday.
		{Centre: lisbon, HasCentre: true, Stops: []Stop{{POIID: "p1", Name: "Museu"}}},
	})
	p.Wait()

	c := got[0][0]
	if c.Holiday != "Dia da Liberdade" || !c.HolidayHoursUnknown || c.Hours != HoursOpen {
		t.Errorf("holiday stop = %+v, want open with the holiday named and its hours marked unknown", c)
	}
	if got[1][0].Holiday != "" || got[1][0].Hours != HoursListed {
		t.Errorf("undated stop = %+v, want hours listed and no holiday", got[1][0])
	}
	if cal.asked != 1 {
		t.Errorf("calendar asked %d times, want once", cal.asked)
	}
}

func TestPlannerStoreErrorLeavesEveryStopUnchecked(t *testing.T) {
	store := &fakeStore{getErr: errors.New("db down")}
	finder := &fakeFinder{}
	p := newPlanner(store, finder, nil)

	got := p.Check(context.Background(), []Day{{
		Centre: lisbon, HasCentre: true, Stops: []Stop{{POIID: "p1", Name: "Museu"}},
	}})
	p.Wait()

	if len(got) != 1 || len(got[0]) != 1 || got[0][0].Status != StatusUnchecked {
		t.Errorf("got %+v, want one unchecked stop", got)
	}
	if len(finder.asked) != 0 {
		t.Errorf("map asked %v, want nothing", finder.asked)
	}
}

func TestPlannerDoesNotAskTwiceForAPlaceAlreadyBeingLookedUp(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{}}
	p := newPlanner(store, &fakeFinder{}, nil)

	// Hold the place as busy, the way a lookup in flight does.
	p.busy["p1"] = true
	p.lookUp(lisbon, []POI{{ID: "p1", Name: "Museu"}})
	p.Wait()

	if len(p.slots) != 0 {
		t.Errorf("a slot was taken for a place already being looked up")
	}
}

func TestPlannerDropsALookupWhenEverySlotIsTaken(t *testing.T) {
	store := &fakeStore{rows: map[string]Verification{}}
	finder := &fakeFinder{}
	p := newPlanner(store, finder, nil)
	for range maxLookups {
		p.slots <- struct{}{}
	}

	p.lookUp(lisbon, []POI{{ID: "p1", Name: "Museu"}})
	p.Wait()

	if len(finder.asked) != 0 || p.busy["p1"] {
		t.Errorf("lookup ran or left the place marked busy with no slot free")
	}
}
