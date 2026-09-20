package calendar

import (
	"testing"
	"time"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/trip"
	"github.com/google/uuid"
)

func TestEventsFromTrips_skipsUndatedDays(t *testing.T) {
	t.Parallel()
	tr := &trip.Trip{
		ID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Title: "Lisbon",
		Days:  []trip.TripDay{{ID: uuid.New(), DayNumber: 1}},
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if got := EventsFromTrips([]*trip.Trip{tr}, from, to); len(got) != 0 {
		t.Fatalf("undated day leaked onto the grid: %+v", got)
	}
}

func TestEventsFromTrips_includesDatedDayInRange(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	start := int32(10 * 60)
	tr := &trip.Trip{
		ID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Title:    "Lisbon weekend",
		CityName: "Lisbon",
		Days: []trip.TripDay{{
			ID:        uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			DayNumber: 1,
			Date:      &day,
			Stops:     []trip.TripStop{{StartMinute: &start, DurationMinutes: intPtr(120)}},
		}},
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	got := EventsFromTrips([]*trip.Trip{tr}, from, to)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Title != "Lisbon weekend" {
		t.Fatalf("title %q", got[0].Title)
	}
	if got[0].Start.Hour() != 10 {
		t.Fatalf("start hour %d, want 10", got[0].Start.Hour())
	}
}

func intPtr(v int32) *int32 { return &v }
