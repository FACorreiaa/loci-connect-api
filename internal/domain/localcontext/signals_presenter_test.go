package localcontext

import (
	"strings"
	"testing"
	"time"

	lcv1 "github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/localcontext"
)

func TestToLocalAlertsProto_MapsKnownKinds(t *testing.T) {
	when := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	got := ToLocalAlertsProto([]Alert{
		{Kind: AlertHoliday, Title: "Republic Day", Detail: "shops closed", Date: &when},
		{Kind: AlertClosure, Title: "Museum closed"},
		{Kind: AlertStrike, Title: "Rail strike"},
	})

	if len(got) != 3 {
		t.Fatalf("got %d alerts", len(got))
	}
	if got[0].Kind != lcv1.AlertKind_ALERT_KIND_HOLIDAY {
		t.Errorf("holiday kind: got %v", got[0].Kind)
	}
	if got[0].Date == nil || !got[0].Date.AsTime().Equal(when) {
		t.Error("the date must survive onto the wire")
	}
	if got[1].Kind != lcv1.AlertKind_ALERT_KIND_CLOSURE {
		t.Errorf("closure kind: got %v", got[1].Kind)
	}
	if got[2].Kind != lcv1.AlertKind_ALERT_KIND_STRIKE {
		t.Errorf("strike kind: got %v", got[2].Kind)
	}
}

// Every kind the domain produces has a wire value as of proto v5.1.1.
func TestToLocalAlertsProto_MapsAllDomainKinds(t *testing.T) {
	cases := map[AlertKind]lcv1.AlertKind{
		AlertClosure:    lcv1.AlertKind_ALERT_KIND_CLOSURE,
		AlertHoliday:    lcv1.AlertKind_ALERT_KIND_HOLIDAY,
		AlertStrike:     lcv1.AlertKind_ALERT_KIND_STRIKE,
		AlertHazard:     lcv1.AlertKind_ALERT_KIND_HAZARD,
		AlertAirQuality: lcv1.AlertKind_ALERT_KIND_AIR_QUALITY,
		AlertTransit:    lcv1.AlertKind_ALERT_KIND_TRANSIT,
		AlertAdvisory:   lcv1.AlertKind_ALERT_KIND_ADVISORY,
	}
	for kind, want := range cases {
		got := ToLocalAlertsProto([]Alert{{Kind: kind, Title: "x"}})
		if len(got) != 1 {
			t.Fatalf("%s: got %d alerts", kind, len(got))
		}
		if got[0].Kind != want {
			t.Errorf("%s: got %v, want %v", kind, got[0].Kind, want)
		}
	}
}

// An unrecognised kind must still reach the user as readable text rather than
// being dropped.
func TestToLocalAlertsProto_UnknownKindSurvivesAsUnspecified(t *testing.T) {
	got := ToLocalAlertsProto([]Alert{{Kind: AlertKind("meteor_strike"), Title: "Meteor"}})
	if len(got) != 1 {
		t.Fatalf("an unknown kind must not be dropped, got %d", len(got))
	}
	if got[0].Kind != lcv1.AlertKind_ALERT_KIND_UNSPECIFIED {
		t.Errorf("got %v, want UNSPECIFIED", got[0].Kind)
	}
	if got[0].Title == "" {
		t.Error("the title must survive so the user can still read it")
	}
}

// The domain treats severity 0 as "unspecified, full weight". Sending a literal
// 0 would tell the client the opposite of what the scorer actually charged.
func TestToLocalAlertsProto_UnsetSeverityIsSentAsFullWeight(t *testing.T) {
	got := ToLocalAlertsProto([]Alert{{Kind: AlertHoliday, Title: "x"}})
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Severity != 1 {
		t.Errorf("severity: got %v, want 1", got[0].Severity)
	}
}

func TestToLocalAlertsProto_CarriesSeverityAndSource(t *testing.T) {
	got := ToLocalAlertsProto([]Alert{{
		Kind: AlertHazard, Title: "Wildfire", Severity: SeverityModerate, Source: SourceGDACS,
	}})
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Severity != 0.5 {
		t.Errorf("severity: got %v, want 0.5", got[0].Severity)
	}
	if got[0].Source != SourceGDACS {
		t.Errorf("source: got %q, want %q", got[0].Source, SourceGDACS)
	}
}

// Located alerts become map pins; country-scoped ones must leave the fields
// absent rather than defaulting to the equator.
func TestToLocalAlertsProto_CoordinatesAreBothOrNeither(t *testing.T) {
	lat, lon := 38.72, -9.14

	located := ToLocalAlertsProto([]Alert{{Kind: AlertHazard, Title: "Wildfire", Lat: &lat, Lon: &lon}})
	if located[0].Latitude == nil || located[0].Longitude == nil {
		t.Fatal("a located hazard must carry both coordinates")
	}
	if *located[0].Latitude != lat || *located[0].Longitude != lon {
		t.Errorf("coordinates: got %v,%v want %v,%v",
			*located[0].Latitude, *located[0].Longitude, lat, lon)
	}

	unplaced := ToLocalAlertsProto([]Alert{{Kind: AlertHoliday, Title: "Republic Day"}})
	if unplaced[0].Latitude != nil || unplaced[0].Longitude != nil {
		t.Error("a country-scoped alert must not carry coordinates")
	}

	// Half-located is treated as unplaced, not drawn at lon=0.
	half := ToLocalAlertsProto([]Alert{{Kind: AlertHazard, Title: "Half", Lat: &lat}})
	if half[0].Latitude != nil || half[0].Longitude != nil {
		t.Error("a half-located alert must send neither coordinate")
	}
}

// LocalAlert.title declares min_len: 1, so an empty title would be rejected by
// the outbound validator and fail the entire RPC. One malformed upstream row
// must not blank every other alert.
func TestToLocalAlertsProto_SkipsUntitledAlerts(t *testing.T) {
	got := ToLocalAlertsProto([]Alert{
		{Kind: AlertHoliday, Title: ""},
		{Kind: AlertHoliday, Title: "Real one"},
	})
	if len(got) != 1 || got[0].Title != "Real one" {
		t.Errorf("got %+v", got)
	}
}

// title/detail declare max_len 300/1000; an over-long field is rejected by the
// validator, so a chatty upstream would otherwise take the response down.
func TestToLocalAlertsProto_TruncatesToProtoLimits(t *testing.T) {
	got := ToLocalAlertsProto([]Alert{{
		Kind:   AlertHoliday,
		Title:  strings.Repeat("t", 500),
		Detail: strings.Repeat("d", 2000),
	}})
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if len(got[0].Title) != 300 {
		t.Errorf("title length: got %d, want 300", len(got[0].Title))
	}
	if len(got[0].Detail) != 1000 {
		t.Errorf("detail length: got %d, want 1000", len(got[0].Detail))
	}
}

func TestToLocalAlertsProto_EmptyIsNil(t *testing.T) {
	if got := ToLocalAlertsProto(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
