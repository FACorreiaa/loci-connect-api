package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/retrieval"
	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/accessibility"
	"github.com/FACorreiaa/loci-connect-api/pkg/routing"
)

type fakeRouter struct {
	mu    sync.Mutex
	calls []routing.Options
	leg   routing.Leg
	err   error
}

func (f *fakeRouter) Route(_ context.Context, _, _ routing.LatLon, opts routing.Options) (routing.Leg, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, opts)
	return f.leg, f.err
}

func stop(name string, day int, lat, lon float64) locitypes.POIDetailedInfo {
	return locitypes.POIDetailedInfo{ID: uuid.New(), Name: name, Day: day, Latitude: lat, Longitude: lon}
}

func lisbonDays() []locitypes.POIDetailedInfo {
	return []locitypes.POIDetailedInfo{
		stop("Castelo de São Jorge", 1, 38.7139, -9.1334),
		stop("Sé de Lisboa", 1, 38.7097, -9.1336),
		stop("Praça do Comércio", 1, 38.7075, -9.1364),
		stop("Torre de Belém", 2, 38.6916, -9.2160),
		stop("Mosteiro dos Jerónimos", 2, 38.6979, -9.2068),
		{Name: "No day", Latitude: 38.7, Longitude: -9.1},
		{Name: "No coordinates", Day: 2},
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestRouteOptionsFor(t *testing.T) {
	manual := routeOptionsFor(&locitypes.AccessibilityNeeds{
		Mobility: locitypes.MobilityManualWheelchair, MaxIncline: locitypes.MaxIncline6, AvoidCobbles: true,
	})
	if manual.Profile != routing.ProfileWheelchair || !manual.AvoidSteps || manual.MaxInclinePct != 6 ||
		manual.MaxSlopedKerbM != 0.03 || !manual.PavedOnly {
		t.Errorf("manual wheelchair options = %+v", manual)
	}
	power := routeOptionsFor(&locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityPowerWheelchair})
	if power.MaxSlopedKerbM != 0 {
		t.Errorf("power chair should use the router's default kerb, got %v", power.MaxSlopedKerbM)
	}
	if routeOptionsFor(&locitypes.AccessibilityNeeds{Stroller: true}).Profile != routing.ProfileWheelchair {
		t.Error("a stroller needs step-free routing too")
	}
	if routeOptionsFor(&locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityWalkingAid}).Profile != routing.ProfileWalking {
		t.Error("a walking aid alone should walk, not use the wheelchair profile")
	}
}

func TestBuildAccessibleRoutesRouted(t *testing.T) {
	router := &fakeRouter{leg: routing.Leg{DistanceM: 950, DurationS: 900, AscentM: 40, Warnings: []string{"Unknown kerb heights"}}}
	l := &ServiceImpl{pedestrianRouter: router, logger: quietLogger()}
	needs := &locitypes.AccessibilityNeeds{
		Mobility: locitypes.MobilityManualWheelchair, MaxWalkMinutesPerLeg: 10, MaxWalkMinutesPerDay: 20, RestEveryMinutes: 12,
	}

	days := l.buildAccessibleRoutes(context.Background(), needs, lisbonDays())
	if len(days) != 2 || days[0].Day != 1 || days[1].Day != 2 {
		t.Fatalf("days = %+v", days)
	}
	if len(days[0].Legs) != 2 || len(days[1].Legs) != 1 {
		t.Fatalf("legs per day = %d, %d; stops without a day or coordinates must be skipped", len(days[0].Legs), len(days[1].Legs))
	}
	leg := days[0].Legs[0]
	if !leg.Routed || leg.DistanceM != 950 || leg.DurationMin != 15 || leg.AscentM != 40 {
		t.Errorf("leg = %+v", leg)
	}
	if leg.SuggestMode != "taxi" {
		t.Error("a 15-minute leg over a 10-minute limit should suggest a taxi")
	}
	wantNotes := []string{"Climbs about 40 m", "Unknown kerb heights", "over your 10-minute limit", "rest interval"}
	for _, want := range wantNotes {
		if !hasNote(leg.Notes, want) {
			t.Errorf("leg notes missing %q: %+v", want, leg.Notes)
		}
	}
	for _, n := range leg.Notes {
		if n.Source != locitypes.AccessibilitySourceRouted {
			t.Errorf("routed leg note %q has source %q", n.Text, n.Source)
		}
	}
	if days[0].TotalWalkMinutes != 30 || len(days[0].Warnings) != 1 || !strings.Contains(days[0].Warnings[0], "20-minute daily limit") {
		t.Errorf("day 1 total %d warnings %v", days[0].TotalWalkMinutes, days[0].Warnings)
	}
	for _, c := range router.calls {
		if c.Profile != routing.ProfileWheelchair || !c.AvoidSteps {
			t.Fatalf("router called with %+v", c)
		}
	}
}

func TestBuildAccessibleRoutesSlowPace(t *testing.T) {
	l := &ServiceImpl{pedestrianRouter: &fakeRouter{leg: routing.Leg{DistanceM: 500, DurationS: 600}}, logger: quietLogger()}
	days := l.buildAccessibleRoutes(context.Background(),
		&locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityWalkingAid}, lisbonDays())
	if got := days[0].Legs[0].DurationMin; got != 15 {
		t.Errorf("10 router minutes at a walking-aid pace = %d, want 15", got)
	}
}

func TestBuildAccessibleRoutesNoRouteIsABarrier(t *testing.T) {
	l := &ServiceImpl{pedestrianRouter: &fakeRouter{err: routing.ErrNoRoute}, logger: quietLogger()}
	days := l.buildAccessibleRoutes(context.Background(),
		&locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityPowerWheelchair}, lisbonDays())
	leg := days[0].Legs[0]
	if leg.Routed {
		t.Error("a leg with no route must not claim to be routed")
	}
	if leg.SuggestMode != "taxi" || !hasNote(leg.Notes, "No step-free route found") {
		t.Fatalf("no-route leg = %+v", leg)
	}
	if leg.Notes[0].Severity != locitypes.AccessibilitySeverityBarrier {
		t.Errorf("no step-free route is a barrier, got %q", leg.Notes[0].Severity)
	}
	if leg.DistanceM == 0 || leg.DurationMin == 0 {
		t.Error("an estimate should still give distance and time")
	}
}

func TestBuildAccessibleRoutesEstimatesHonestly(t *testing.T) {
	for name, l := range map[string]*ServiceImpl{
		"no router":          {logger: quietLogger()},
		"router unavailable": {pedestrianRouter: &fakeRouter{err: errors.Join(routing.ErrUnavailable, errors.New("503"))}, logger: quietLogger()},
	} {
		t.Run(name, func(t *testing.T) {
			days := l.buildAccessibleRoutes(context.Background(),
				&locitypes.AccessibilityNeeds{StepFreeOnly: true}, lisbonDays())
			leg := days[0].Legs[0]
			if leg.Routed {
				t.Fatal("an estimate must say routed=false")
			}
			if !hasNote(leg.Notes, "not checked for steps or slopes") {
				t.Fatalf("estimate not labelled: %+v", leg.Notes)
			}
			if leg.Notes[0].Source != locitypes.AccessibilitySourceAIEstimate || leg.Notes[0].Severity != locitypes.AccessibilitySeverityCaution {
				t.Errorf("unchecked leg for a step-free traveller = %+v", leg.Notes[0])
			}
			// Castle to cathedral is ~470 m straight; with the detour factor ~610 m.
			if leg.DistanceM < 500 || leg.DistanceM > 700 {
				t.Errorf("estimated distance %d m", leg.DistanceM)
			}
		})
	}
}

func TestBuildAccessibleRoutesNothingWithoutNeeds(t *testing.T) {
	router := &fakeRouter{}
	l := &ServiceImpl{pedestrianRouter: router, logger: quietLogger()}
	if got := l.buildAccessibleRoutes(context.Background(), &locitypes.AccessibilityNeeds{}, lisbonDays()); got != nil {
		t.Fatalf("routes built for a traveller without needs: %+v", got)
	}
	if len(router.calls) != 0 {
		t.Fatal("router called for a traveller without needs")
	}
}

type fakeCrowd map[uuid.UUID][]retrieval.VerifiedAccessFact

func (f fakeCrowd) VerifiedAccessibility(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]retrieval.VerifiedAccessFact, error) {
	out := map[uuid.UUID][]retrieval.VerifiedAccessFact{}
	for _, id := range ids {
		if v, ok := f[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

type fakeOpenData struct {
	mu    sync.Mutex
	calls int
	facts map[string]*accessibility.PlaceFacts
}

func (f *fakeOpenData) Lookup(_ context.Context, _, _ float64, name string) (*accessibility.PlaceFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.facts[name], nil
}

func ptr[T any](v T) *T { return &v }

func TestEnrichAccessibility(t *testing.T) {
	castle := stop("Castelo de São Jorge", 1, 38.7139, -9.1334)
	castle.AccessibilityNotes = []locitypes.AccessibilityNote{{Text: "Steep approach", Severity: "caution", Source: locitypes.AccessibilitySourceAIEstimate}}
	museum := stop("Museu do Azulejo", 1, 38.7246, -9.1134)

	open := &fakeOpenData{facts: map[string]*accessibility.PlaceFacts{
		"Museu do Azulejo": {
			Wheelchair: accessibility.WheelchairNo, EntranceLevel: ptr(false), AccessibleToilet: ptr(false),
			SourceName: "OpenStreetMap", Licence: "ODbL-1.0",
		},
	}}
	l := &ServiceImpl{
		crowdAccess: fakeCrowd{castle.ID: {
			{Token: "step_free", ContributorCount: 3, VerifiedAt: time.Now()},
			{Token: "unknown_token", ContributorCount: 3, VerifiedAt: time.Now()},
		}},
		placeAccessibility: open,
		logger:             quietLogger(),
	}
	needs := &locitypes.AccessibilityNeeds{Mobility: locitypes.MobilityManualWheelchair, NeedsAccessibleToilet: true}

	itinerary := []locitypes.POIDetailedInfo{castle, museum}
	general := []locitypes.POIDetailedInfo{museum}
	l.enrichAccessibility(context.Background(), needs, itinerary, general)

	if open.calls != 2 {
		t.Errorf("open-data lookups = %d, want one per distinct place", open.calls)
	}

	cn := itinerary[0].AccessibilityNotes
	if len(cn) != 2 || cn[0].Source != locitypes.AccessibilitySourceVerifiedCrowd ||
		!strings.Contains(cn[0].Text, "confirmed by 3 Loci travellers") {
		t.Fatalf("castle notes = %+v (verified first, AI estimate kept after, unknown token dropped)", cn)
	}
	if cn[1].Source != locitypes.AccessibilitySourceAIEstimate {
		t.Errorf("model estimate should follow the evidence, got %+v", cn[1])
	}

	mn := itinerary[1].AccessibilityNotes
	if len(mn) != 3 {
		t.Fatalf("museum notes = %+v", mn)
	}
	for _, n := range mn {
		if n.Source != locitypes.AccessibilitySourceOpenData || n.SourceLicence != "ODbL-1.0" || !strings.Contains(n.Text, "(OpenStreetMap)") {
			t.Errorf("open-data note lost attribution: %+v", n)
		}
		if n.Severity != locitypes.AccessibilitySeverityBarrier {
			t.Errorf("%q should be a barrier for a wheelchair user who needs an accessible toilet", n.Text)
		}
	}
	if len(general[0].AccessibilityNotes) != 3 {
		t.Error("the same place in another list did not get the notes")
	}
}

func TestEnrichAccessibilitySkipsWithoutNeeds(t *testing.T) {
	open := &fakeOpenData{}
	l := &ServiceImpl{placeAccessibility: open, logger: quietLogger()}
	l.enrichAccessibility(context.Background(), nil, lisbonDays())
	if open.calls != 0 {
		t.Fatal("open data looked up for a traveller without needs")
	}
}

func hasNote(notes []locitypes.AccessibilityNote, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n.Text, substr) {
			return true
		}
	}
	return false
}
