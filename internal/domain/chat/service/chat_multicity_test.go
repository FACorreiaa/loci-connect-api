package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/chat/common"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/multicity"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func twoCityRoute() *multiCityRoute {
	route := multicity.PlanFixed(multicity.Input{MultiModal: true}, []multicity.FixedStop{
		{City: multicity.City{Name: "Lisbon", Lat: 38.72, Lon: -9.14}, Nights: 2},
		{City: multicity.City{Name: "Porto", Lat: 41.15, Lon: -8.61}, Nights: 1},
	})
	return &multiCityRoute{
		Route: route, Message: "itinerary",
		Stops: []multiCityStop{
			{Index: 0, CityName: "Lisbon", Lat: 38.72, Lon: -9.14, Days: []int{1, 2}, Nights: 2, SessionID: uuid.New()},
			{Index: 1, CityName: "Porto", Lat: 41.15, Lon: -8.61, Days: []int{3}, Nights: 1, SessionID: uuid.New()},
		},
	}
}

func TestForwardStopEvent(t *testing.T) {
	ev, keep := forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeItinerary}, 1)
	if !keep || ev.StopIndex == nil || *ev.StopIndex != 1 {
		t.Fatalf("itinerary should be tagged with stop 1: %+v", ev)
	}
	if _, keep := forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeComplete}, 0); keep {
		t.Fatal("a city's COMPLETE must not reach the client; the run completes once")
	}
	ev, keep = forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeStart, Data: locitypes.StreamStartData{SessionID: "s1"}}, 1)
	if !keep || ev.Type != locitypes.EventTypeProgress {
		t.Fatalf("a city's START becomes progress (one START names the run): %+v", ev)
	}
	ev, _ = forwardStopEvent(locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "x"}, 0)
	if ev.IsTerminal() {
		t.Fatal("a city's ERROR must not be terminal")
	}
}

func TestMergeStopTrips(t *testing.T) {
	r := twoCityRoute()
	lisbon := &trip.Trip{Days: []trip.TripDay{{DayNumber: 1, Stops: []trip.TripStop{{Name: "Belém"}}}, {DayNumber: 2}}}
	porto := &trip.Trip{Days: []trip.TripDay{{DayNumber: 1, Stops: []trip.TripStop{{Name: "Ribeira"}}}}}
	user := uuid.New()

	got := mergeStopTrips(user, r, []*trip.Trip{lisbon, porto})

	if got.Title != "Lisbon + Porto" || got.CityName != "Lisbon" || got.UserID != user {
		t.Fatalf("header = %q / %q", got.Title, got.CityName)
	}
	if len(got.Days) != 3 {
		t.Fatalf("expected 3 days, got %d", len(got.Days))
	}
	if d := got.Days[2]; d.DayNumber != 3 || d.CityName != "Porto" || !d.TravelDay || len(d.Stops) != 1 || d.Stops[0].Name != "Ribeira" {
		t.Fatalf("day 3 = %+v", d)
	}
	if got.Days[1].TravelDay {
		t.Fatal("day 2 is not a travel day")
	}
	if len(got.Legs) != 1 || got.Legs[0].AfterDay != 2 || got.Legs[0].Mode != "train" {
		t.Fatalf("legs = %+v", got.Legs)
	}
	if len(got.Cities) != 2 || got.Cities[1].SessionID == nil || *got.Cities[1].SessionID != r.Stops[1].SessionID {
		t.Fatalf("cities = %+v", got.Cities)
	}
	if got.SourceSessionID == nil || *got.SourceSessionID != r.Stops[0].SessionID.String() {
		t.Fatal("the parent trip's source session is the first city's")
	}
}

// Review Focus #2: a city that fails still gets its days, and the others keep theirs.
func TestMergeStopTrips_FailedCityKeepsItsDays(t *testing.T) {
	r := twoCityRoute()
	got := mergeStopTrips(uuid.New(), r, []*trip.Trip{nil, {Days: []trip.TripDay{{DayNumber: 1}}}})
	if len(got.Days) != 3 || got.Days[0].CityName != "Lisbon" || len(got.Days[0].Stops) != 0 {
		t.Fatalf("days = %+v", got.Days)
	}
}

func TestProcessMultiCity_EventOrderAndFailureIsolation(t *testing.T) {
	events := make(chan locitypes.StreamEvent, 200)
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		if cc.CityName == "Porto" {
			cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "porto failed"}
			return nil, errors.New("porto failed")
		}
		if !cc.StopRun || !cc.SuppressTripSave || cc.PresetTripDays != 2 || cc.PresetSessionID == uuid.Nil {
			t.Errorf("child context not preset: %+v", cc)
		}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeStart, Data: locitypes.StreamStartData{SessionID: cc.PresetSessionID.String()}}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, Data: locitypes.AiCityResponse{}}
		cc.EventCh <- locitypes.StreamEvent{Type: locitypes.EventTypeComplete}
		return &locitypes.AiCityResponse{}, nil
	}
	err := l.processMultiCity(common.ChatContext{Ctx: context.Background(), UserID: uuid.New(), EventCh: events}, twoCityRoute())
	close(events)
	if err != nil {
		t.Fatalf("one city failing must not fail the run: %v", err)
	}

	var types []string
	var stopsOnError []int
	for ev := range events {
		types = append(types, ev.Type)
		if ev.Type == locitypes.EventTypeError && ev.StopIndex != nil {
			stopsOnError = append(stopsOnError, *ev.StopIndex)
		}
	}
	if len(types) < 3 || types[0] != locitypes.EventTypeStart || types[1] != locitypes.EventTypeRoute {
		t.Fatalf("stream must open START, ROUTE; got %v", types)
	}
	if types[len(types)-1] != locitypes.EventTypeComplete {
		t.Fatalf("stream must end COMPLETE; got %v", types)
	}
	starts, completes := 0, 0
	for _, ty := range types {
		switch ty {
		case locitypes.EventTypeStart:
			starts++
		case locitypes.EventTypeComplete:
			completes++
		}
	}
	if starts != 1 || completes != 1 {
		t.Fatalf("exactly one START and one COMPLETE, got %d/%d in %v", starts, completes, types)
	}
	if len(stopsOnError) != 1 || stopsOnError[0] != 1 {
		t.Fatalf("Porto's error must be tagged stop 1: %v", stopsOnError)
	}
}

func TestProcessMultiCity_AllFail(t *testing.T) {
	events := make(chan locitypes.StreamEvent, 50)
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) { return nil, errors.New("down") }
	err := l.processMultiCity(common.ChatContext{Ctx: context.Background(), EventCh: events}, twoCityRoute())
	close(events)
	if err == nil {
		t.Fatal("every city failing is a failed run")
	}
	var last locitypes.StreamEvent
	for ev := range events {
		last = ev
	}
	if !last.IsTerminal() || last.Type != locitypes.EventTypeError {
		t.Fatalf("expected a terminal untagged ERROR last, got %+v", last)
	}
}

func TestProcessUnified_SingleCityNeverDispatches(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.cityResolver = iberia
	called := 0
	l.runCityFn = func(common.ChatContext) (*locitypes.AiCityResponse, error) { called++; return nil, nil }
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Lisbon"}}, Message: "itinerary"})
	l.cache.Set(tripCitiesCacheKey("3 days in Lisbon"), string(raw), time.Hour)
	_ = l.ProcessUnifiedChatMessageStream(common.ChatContext{Ctx: context.Background(), Message: "3 days in Lisbon", EventCh: make(chan locitypes.StreamEvent, 10)})
	if called != 1 {
		t.Fatalf("single-city turn should run exactly one city, ran %d", called)
	}
}

func TestProcessUnified_TwoCitiesDispatch(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.cityResolver = iberia
	var cities []string
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		cities = append(cities, cc.CityName)
		return &locitypes.AiCityResponse{}, nil
	}
	msg := "Lisbon for 2 days then Porto for 1"
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Lisbon", Days: 2}, {Name: "Porto", Days: 1}}, Ordered: true, Message: "trip"})
	l.cache.Set(tripCitiesCacheKey(msg), string(raw), time.Hour)
	if err := l.ProcessUnifiedChatMessageStream(common.ChatContext{Ctx: context.Background(), Message: msg, MultiCityCapable: true, EventCh: make(chan locitypes.StreamEvent, 100)}); err != nil {
		t.Fatal(err)
	}
	if len(cities) != 2 || cities[0] != "Lisbon" || cities[1] != "Porto" {
		t.Fatalf("expected Lisbon then Porto, ran %v", cities)
	}
}

// A city's places must land on that city's own days: 12 places over a 2-day
// stop is 2 days of places, not 3 days at four a day with the third lost.
func TestStopTrip_UsesTheStopsDays(t *testing.T) {
	var pois []locitypes.POIDetailedInfo
	for i := 0; i < 12; i++ {
		pois = append(pois, locitypes.POIDetailedInfo{ID: uuid.New(), Name: "Place"})
	}
	data := &locitypes.AiCityResponse{PointsOfInterest: pois}
	s := multiCityStop{CityName: "Lisbon", Days: []int{1, 2}, SessionID: uuid.New()}

	tr := stopTrip(common.ChatContext{UserID: uuid.New()}, s, data)

	if tr == nil || len(tr.Days) != 2 {
		t.Fatalf("expected 2 days, got %+v", tr)
	}
	total := 0
	for _, d := range tr.Days {
		total += len(d.Stops)
	}
	if total != 12 {
		t.Fatalf("expected all 12 places kept, got %d", total)
	}
}

func TestTurnCollector_KeepsEveryCity(t *testing.T) {
	one, zero := 1, 0
	s0, s1 := uuid.New(), uuid.New()
	var c turnCollector
	c.observe(locitypes.StreamEvent{Type: locitypes.EventTypeRoute, Data: locitypes.StreamRouteData{Outline: "Lisbon → Porto"}})
	c.observe(locitypes.StreamEvent{Type: locitypes.EventTypeProgress, Message: "city_started", StopIndex: &zero})
	c.observe(locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, StopIndex: &one, Data: locitypes.AiCityResponse{SessionID: s1, GeneralCityData: locitypes.GeneralCityData{City: "Porto"}}})
	c.observe(locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, StopIndex: &zero, Data: &locitypes.AiCityResponse{SessionID: s0, GeneralCityData: locitypes.GeneralCityData{City: "Lisbon"}}})

	resp := c.response(uuid.Nil)
	if resp.RouteOutline != "Lisbon → Porto" || len(resp.Cities) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Cities[0].GeneralCityData.City != "Lisbon" || resp.SessionID != s0 {
		t.Fatalf("cities must be in stop order and the session is the first city's: %+v", resp)
	}
	if resp.Message != "" {
		t.Fatalf("a city's progress text leaked into the message: %q", resp.Message)
	}
}

// Review #1: the whole run fits inside the run store's staleness window, so a
// long trip is never recorded as failed while it is still working.
func TestCityDeadline_FitsTheRunBudget(t *testing.T) {
	if got := cityDeadline(multiCityBudget, 2); got != cityBudget {
		t.Fatalf("two cities get the full per-city budget, got %v", got)
	}
	if got := cityDeadline(multiCityBudget, 5); got*5 > multiCityBudget {
		t.Fatalf("five cities must fit the run budget: %v each", got)
	}
	if got := cityDeadline(30*time.Second, 2); got < minCityBudget {
		t.Fatalf("a city always gets at least %v, got %v", minCityBudget, got)
	}
	if multiCityBudget >= runs.StaleAfter {
		t.Fatalf("multi-city budget %v must stay under run staleness %v", multiCityBudget, runs.StaleAfter)
	}
}

// Review #2: a city whose own deadline expired cannot send its ERROR (its
// context is gone), so the run sends it for the city on the stream's context.
func TestProcessMultiCity_SilentCityFailureIsReported(t *testing.T) {
	events := make(chan locitypes.StreamEvent, 100)
	l := newStreamService(t, &TestLLMClient{})
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		if cc.CityName == "Porto" {
			return nil, context.DeadlineExceeded // no event sent: the city's context had expired
		}
		return &locitypes.AiCityResponse{}, nil
	}
	if err := l.processMultiCity(common.ChatContext{Ctx: context.Background(), EventCh: events}, twoCityRoute()); err != nil {
		t.Fatal(err)
	}
	close(events)
	found := false
	for ev := range events {
		if ev.Type == locitypes.EventTypeError && ev.StopIndex != nil && *ev.StopIndex == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("Porto's failure must reach the client as a stop-tagged ERROR")
	}
}

// Review #3: "Atlantis and Lisbon" is a single-city trip to Lisbon — not to
// Atlantis because it was named first — and the city is not re-extracted.
func TestProcessUnified_SurvivorIsTheCity(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.cityResolver = iberia
	var got []common.ChatContext
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) { got = append(got, cc); return nil, nil }
	msg := "Atlantis and Lisbon for a weekend"
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Atlantis"}, {Name: "Lisbon"}}, Message: "weekend"})
	l.cache.Set(tripCitiesCacheKey(msg), string(raw), time.Hour)
	events := make(chan locitypes.StreamEvent, 10)
	_ = l.ProcessUnifiedChatMessageStream(common.ChatContext{Ctx: context.Background(), Message: msg, MultiCityCapable: true, EventCh: events})
	if len(got) != 1 || got[0].CityName != "Lisbon" || !got[0].CityFixed {
		t.Fatalf("expected one run for Lisbon with the city fixed, got %+v", got)
	}
}

// Review #6: a client that does not know multi-city streams (an old app or a
// cached web bundle) keeps today's single-city answer for free text.
func TestProcessUnified_OldClientStaysSingleCity(t *testing.T) {
	l := newStreamService(t, &TestLLMClient{})
	l.cityResolver = iberia
	var cities []string
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		cities = append(cities, cc.CityName)
		return &locitypes.AiCityResponse{}, nil
	}
	msg := "Lisbon for 2 days then Porto for 1"
	raw, _ := json.Marshal(TripCities{Cities: []ExtractedCity{{Name: "Lisbon", Days: 2}, {Name: "Porto", Days: 1}}, Ordered: true, Message: "trip"})
	l.cache.Set(tripCitiesCacheKey(msg), string(raw), time.Hour)
	_ = l.ProcessUnifiedChatMessageStream(common.ChatContext{Ctx: context.Background(), Message: msg, EventCh: make(chan locitypes.StreamEvent, 100)})
	if len(cities) != 1 {
		t.Fatalf("an old client must get one city, ran %v", cities)
	}
}

func threeCityRoute() *multiCityRoute {
	r := twoCityRoute()
	r.Stops = append(r.Stops, multiCityStop{Index: 2, CityName: "Coimbra", Lat: 40.21, Lon: -8.43, Days: []int{4}, Nights: 1, SessionID: uuid.New()})
	return r
}

// Two cities at a time: the trip is ready in about half the wall time, and at
// most concurrency cities hold the pod's LLM slots at once.
func TestProcessMultiCity_RunsCitiesTwoAtATime(t *testing.T) {
	events := make(chan locitypes.StreamEvent, 300)
	l := newStreamService(t, &TestLLMClient{})
	l.SetMultiCityConcurrency(2)

	var mu sync.Mutex
	running, peak := 0, 0
	var started []string
	release := make(chan struct{})
	l.runCityFn = func(cc common.ChatContext) (*locitypes.AiCityResponse, error) {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		started = append(started, cc.CityName)
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
		return &locitypes.AiCityResponse{}, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- l.processMultiCity(common.ChatContext{Ctx: context.Background(), EventCh: events}, threeCityRoute())
	}()
	// Let the first wave start, then free them one by one.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(started)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("two cities never ran at the same time")
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	close(events)
	if peak != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak)
	}
	// Waves go in route order; within a wave the two start together.
	if len(started) != 3 || started[2] != "Coimbra" {
		t.Fatalf("the third city waits for a slot, got %v", started)
	}
	completes := 0
	for ev := range events {
		if ev.Type == locitypes.EventTypeComplete {
			completes++
		}
	}
	if completes != 1 {
		t.Fatalf("one COMPLETE, got %d", completes)
	}
}

func TestMultiCityWaves(t *testing.T) {
	cases := []struct{ left, conc, want int }{{5, 2, 3}, {4, 2, 2}, {1, 2, 1}, {3, 1, 3}, {0, 2, 1}}
	for _, c := range cases {
		if got := waves(c.left, c.conc); got != c.want {
			t.Errorf("waves(%d,%d) = %d, want %d", c.left, c.conc, got, c.want)
		}
	}
}
