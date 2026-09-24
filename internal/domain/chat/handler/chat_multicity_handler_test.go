package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	chatv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/chat"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/runs"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

func intPtr(i int) *int { return &i }

func TestStreamEvent_IsTerminal(t *testing.T) {
	cases := []struct {
		ev   locitypes.StreamEvent
		want bool
	}{
		{locitypes.StreamEvent{Type: locitypes.EventTypeComplete}, true},
		{locitypes.StreamEvent{Type: locitypes.EventTypeError}, true},
		{locitypes.StreamEvent{Type: locitypes.EventTypeError, StopIndex: intPtr(1)}, false},
		{locitypes.StreamEvent{Type: locitypes.EventTypeItinerary, StopIndex: intPtr(0)}, false},
		{locitypes.StreamEvent{Type: locitypes.EventTypeRoute}, false},
	}
	for _, c := range cases {
		if got := c.ev.IsTerminal(); got != c.want {
			t.Errorf("%s stop=%v: IsTerminal = %v, want %v", c.ev.Type, c.ev.StopIndex, got, c.want)
		}
	}
}

func TestMapEventToProto_RouteAndStopIndex(t *testing.T) {
	h := &ChatHandler{}
	ev := locitypes.StreamEvent{
		Type: locitypes.EventTypeRoute,
		Data: locitypes.StreamRouteData{
			Stops: []locitypes.StreamRouteStop{
				{Index: 0, CityName: "Lisbon", SessionID: "s0", DayNumbers: []int{1, 2, 3}},
				{Index: 1, CityName: "Porto", SessionID: "s1", DayNumbers: []int{4, 5}},
			},
			Legs:    []locitypes.StreamRouteLeg{{AfterDay: 3, FromName: "Lisbon", ToName: "Porto", DistanceKm: 274, DurationMins: 194, Mode: "train"}},
			Outline: "Lisbon (3 days) → Porto (2 days) · ≈3h14 travel in total",
			Dropped: []locitypes.StreamDroppedStop{{CityName: "Atlantis", Reason: "we couldn't find this city"}},
			TripID:  "trip-1",
		},
	}
	got, err := h.mapEventToProto(context.Background(), ev, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetEventType() != chatv1.StreamEventType_STREAM_EVENT_TYPE_ROUTE {
		t.Fatalf("event type = %v", got.GetEventType())
	}
	r := got.GetRoute()
	if len(r.GetStops()) != 2 || r.GetStops()[1].GetSessionId() != "s1" || len(r.GetStops()[1].GetDayNumbers()) != 2 {
		t.Fatalf("stops = %+v", r.GetStops())
	}
	if len(r.GetLegs()) != 1 || r.GetLegs()[0].GetMode() != "train" || r.GetTripId() != "trip-1" {
		t.Fatalf("legs/trip = %+v %q", r.GetLegs(), r.GetTripId())
	}
	if len(r.GetDropped()) != 1 || r.GetDropped()[0].GetCityName() != "Atlantis" {
		t.Fatalf("dropped = %+v", r.GetDropped())
	}
	if got.StopIndex != nil {
		t.Fatal("a route event is not about one city")
	}

	tagged := locitypes.StreamEvent{Type: locitypes.EventTypeError, Error: "boom", StopIndex: intPtr(1)}
	p, err := h.mapEventToProto(context.Background(), tagged, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if p.StopIndex == nil || p.GetStopIndex() != 1 {
		t.Fatalf("stop_index not carried: %+v", p)
	}
}

// Review Focus #3 / review #1: the stream outlives the planner's own budget
// but stays inside the run store's staleness window.
func TestStreamBudget(t *testing.T) {
	if streamBudget() >= runs.StaleAfter {
		t.Fatalf("stream budget %v must stay under run staleness %v", streamBudget(), runs.StaleAfter)
	}
	if streamBudget() < 9*time.Minute {
		t.Fatalf("stream budget %v must cover a multi-city run", streamBudget())
	}
}

func TestMultiCityCapable(t *testing.T) {
	h := http.Header{}
	if multiCityCapable(h) {
		t.Fatal("no header: not capable")
	}
	h.Set(featuresHeader, "resume, multi-city")
	if !multiCityCapable(h) {
		t.Fatal("header lists multi-city: capable")
	}
}
