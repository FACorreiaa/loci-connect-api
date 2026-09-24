package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/city"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

type fakeResolver map[string][2]float64

func (f fakeResolver) Resolve(_ context.Context, q city.ResolveQuery) (*city.Resolved, error) {
	ll, ok := f[strings.ToLower(q.Name)]
	if !ok {
		return nil, city.ErrCityUnresolvable
	}
	return &city.Resolved{City: locitypes.CityDetail{Name: q.Name}, Lat: ll[0], Lon: ll[1]}, nil
}

var iberia = fakeResolver{
	"lisbon": {38.72, -9.14}, "porto": {41.15, -8.61}, "seville": {37.39, -5.98},
	"coimbra": {40.21, -8.43}, "rome": {41.90, 12.50}, "madrid": {40.42, -3.70},
}

func names(r *multiCityRoute) []string {
	out := []string{}
	for _, s := range r.Stops {
		out = append(out, s.CityName)
	}
	return out
}

func TestBuildRoute_OrderedWithDaysKeepsTravellerPlan(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities:  []ExtractedCity{{Name: "Porto", Days: 2}, {Name: "Lisbon", Days: 3}},
		Ordered: true, TotalDays: 5, DaysStated: true, Message: "trip",
	})
	if err != nil || r == nil {
		t.Fatalf("route = %v, err = %v", r, err)
	}
	if got := names(r); got[0] != "Porto" || got[1] != "Lisbon" {
		t.Fatalf("order changed: %v", got)
	}
	if len(r.Stops[0].Days) != 2 || len(r.Stops[1].Days) != 3 || r.Stops[1].Days[0] != 3 {
		t.Fatalf("days = %v / %v", r.Stops[0].Days, r.Stops[1].Days)
	}
}

// Review Focus #1: no duration stated must not starve the route of days.
func TestBuildRoute_NoDurationWidensWindow(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities:    []ExtractedCity{{Name: "Lisbon"}, {Name: "Porto"}, {Name: "Seville"}},
		TotalDays: 2, DaysStated: false, Message: "trip",
	})
	if err != nil || r == nil {
		t.Fatalf("route = %v, err = %v", r, err)
	}
	if len(r.Stops) != 3 {
		t.Fatalf("expected all three cities kept, got %v (dropped %+v)", names(r), r.Dropped)
	}
	if r.Route.Days[len(r.Route.Days)-1].DayNumber < 6 {
		t.Fatalf("window should be at least 2 days per city")
	}
}

func TestBuildRoute_SuggestOrderReorders(t *testing.T) {
	r, _ := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities:  []ExtractedCity{{Name: "Lisbon", Days: 2}, {Name: "Seville", Days: 2}, {Name: "Porto", Days: 2}},
		Ordered: true, SuggestOrder: true, TotalDays: 6, DaysStated: true, Message: "trip",
	})
	if r == nil {
		t.Fatal("nil route")
	}
	if got := names(r); got[0] != "Lisbon" || got[1] != "Porto" {
		t.Fatalf("expected nearest-neighbour Lisbon → Porto → Seville, got %v", got)
	}
}

// Review Focus #5.
func TestBuildRoute_UnresolvableIsDroppedNotFatal(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities:  []ExtractedCity{{Name: "Lisbon", Days: 2}, {Name: "Atlantis", Days: 2}, {Name: "Porto", Days: 2}},
		Ordered: true, TotalDays: 6, DaysStated: true, Message: "trip",
	})
	if err != nil || r == nil || len(r.Stops) != 2 {
		t.Fatalf("expected Lisbon + Porto, got %v err %v", r, err)
	}
	if len(r.Dropped) != 1 || r.Dropped[0].CityName != "Atlantis" || r.Dropped[0].Reason == "" {
		t.Fatalf("dropped = %+v", r.Dropped)
	}
}

func TestBuildRoute_OneRealCityFallsBack(t *testing.T) {
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities: []ExtractedCity{{Name: "Lisbon"}, {Name: "Atlantis"}}, Message: "trip", TotalDays: 3,
	})
	if err != nil || r != nil {
		t.Fatalf("expected nil route (single-city fallback), got %v err %v", r, err)
	}
}

func TestBuildRoute_NoneResolve(t *testing.T) {
	_, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{
		Cities: []ExtractedCity{{Name: "Atlantis"}, {Name: "Lemuria"}}, Message: "trip", TotalDays: 3,
	})
	if !errors.Is(err, ErrNoCitiesResolved) {
		t.Fatalf("err = %v, want ErrNoCitiesResolved", err)
	}
}

func TestBuildRoute_CapsAtFive(t *testing.T) {
	six := []ExtractedCity{{Name: "Lisbon"}, {Name: "Porto"}, {Name: "Seville"}, {Name: "Coimbra"}, {Name: "Madrid"}, {Name: "Rome"}}
	r, err := buildMultiCityRoute(context.Background(), iberia, routeRequest{Cities: six, TotalDays: 14, DaysStated: true, Message: "trip"})
	if err != nil || r == nil || len(r.Stops) > 5 {
		t.Fatalf("expected at most 5 stops, got %v err %v", r, err)
	}
}
