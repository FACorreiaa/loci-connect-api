package trip

import (
	"strings"
	"testing"
	"time"

	tripv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/trip"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ptrI32(v int32) *int32 { return &v }

func TestBuildICS(t *testing.T) {
	date := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	trip := &Trip{
		Title: "Lisbon Weekend",
		Days: []TripDay{{
			ID:        uuid.New(),
			DayNumber: 1,
			Date:      &date,
			Stops: []TripStop{
				{ID: uuid.New(), Name: "Jerónimos Monastery", StartMinute: ptrI32(600), DurationMinutes: ptrI32(90), Notes: "Book ahead; skip line"},
				{ID: uuid.New(), Name: "No time stop"}, // skipped (no start_minute)
			},
		}},
	}

	ics := buildICS(trip)

	for _, want := range []string{
		"BEGIN:VCALENDAR", "END:VCALENDAR",
		"BEGIN:VEVENT", "END:VEVENT",
		"DTSTART:20260801T100000Z", // 600 min = 10:00
		"DTEND:20260801T113000Z",   // +90 min
		"SUMMARY:Jerónimos Monastery",
		"DESCRIPTION:Book ahead\\; skip line", // ';' escaped
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("ICS missing %q\n---\n%s", want, ics)
		}
	}
	if strings.Contains(ics, "No time stop") {
		t.Error("stop without start_minute should be skipped")
	}
	if n := strings.Count(ics, "BEGIN:VEVENT"); n != 1 {
		t.Errorf("expected 1 VEVENT, got %d", n)
	}
}

func TestBuildTripMarkdown(t *testing.T) {
	start := int32(12 * 60)
	dur := int32(90)
	trip := &Trip{
		Title:    "Weekend in Lisbon",
		CityName: "Lisbon",
		Constraints: TripConstraint{
			Pace:      paceModerate,
			Mobility:  "walking",
			Interests: []string{"food"},
		},
		Days: []TripDay{{
			DayNumber: 1,
			Stops: []TripStop{
				{Name: "Time Out Market", StartMinute: &start, DurationMinutes: &dur, Notes: "Highly rated market."},
			},
		}},
	}
	md := buildTripMarkdown(trip)
	for _, want := range []string{
		"# Weekend in Lisbon",
		"Lisbon",
		"## Day 1",
		"Time Out Market",
		"Why this:",
		"Highly rated market.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n%s", want, md)
		}
	}
}

func TestTripProtoRoundTrip(t *testing.T) {
	uid := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	src := &tripv1.TripDraft{
		Id:          uuid.New().String(),
		CityName:    "Porto",
		Title:       "Food Tour",
		Constraints: &tripv1.TripConstraint{BudgetLevel: ptrI32(3), Pace: tripv1.TripPace_TRIP_PACE_PACKED, Mobility: strPtr("walking"), Interests: []string{"food", "wine"}},
		Days: []*tripv1.TripDay{{
			Id:        uuid.New().String(),
			DayNumber: 1,
			Date:      timestamppb.New(now),
			Stops: []*tripv1.TripStop{{
				Id: uuid.New().String(), PoiId: "poi-1", OrderIndex: 0, Name: "Cafe",
				StartMinute: ptrI32(540), DurationMinutes: ptrI32(60), Notes: "coffee", BookingUrl: strPtr("https://example.com/book"),
			}},
		}},
	}

	dom, err := tripFromProto(src, uid)
	if err != nil {
		t.Fatalf("tripFromProto: %v", err)
	}
	if dom.UserID != uid || dom.Title != "Food Tour" || dom.CityName != "Porto" {
		t.Fatalf("core fields lost: %+v", dom)
	}
	if dom.Constraints.BudgetLevel == nil || *dom.Constraints.BudgetLevel != 3 ||
		dom.Constraints.Pace != int32(tripv1.TripPace_TRIP_PACE_PACKED) ||
		dom.Constraints.Mobility != "walking" || len(dom.Constraints.Interests) != 2 {
		t.Fatalf("constraints lost: %+v", dom.Constraints)
	}
	if len(dom.Days) != 1 || len(dom.Days[0].Stops) != 1 {
		t.Fatalf("days/stops lost: %+v", dom.Days)
	}
	st := dom.Days[0].Stops[0]
	if st.POIID != "poi-1" || st.Name != "Cafe" || st.StartMinute == nil || *st.StartMinute != 540 ||
		st.BookingURL == nil || *st.BookingURL != "https://example.com/book" {
		t.Fatalf("stop fields lost: %+v", st)
	}

	// domain -> proto keeps the same shape.
	back := tripToProto(dom)
	if back.GetTitle() != "Food Tour" || back.GetConstraints().GetBudgetLevel() != 3 ||
		len(back.GetDays()) != 1 || back.GetDays()[0].GetStops()[0].GetName() != "Cafe" {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
}

func TestSortStopsByOrder(t *testing.T) {
	stops := []TripStop{
		{Name: "c", OrderIndex: 2},
		{Name: "a", OrderIndex: 0},
		{Name: "b", OrderIndex: 1},
	}
	sortStopsByOrder(stops)
	got := stops[0].Name + stops[1].Name + stops[2].Name
	if got != "abc" {
		t.Fatalf("expected abc, got %s", got)
	}
}

func strPtr(s string) *string { return &s }
