package localcontext

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type fixedCountry struct {
	code string
	err  error
}

func (f fixedCountry) CountryCode(context.Context, float64, float64) (string, error) {
	return f.code, f.err
}

func sept(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }

func TestHolidayCalendar(t *testing.T) {
	source, hits := nagerSource(t, nagerFixture, http.StatusOK)
	cal := NewHolidayCalendar(fixedCountry{code: "PT"}, source)
	ctx := context.Background()

	cases := []struct {
		name string
		day  time.Time
		want string
	}{
		{"a nationwide public holiday is named in the local language", sept(2), "Dia Publico"},
		{"an optional day is not a holiday", sept(3), ""},
		{"a regional holiday is not assumed to apply", sept(4), ""},
		{"an ordinary day", sept(5), ""},
	}
	for _, tc := range cases {
		if got := cal.Holiday(ctx, 38.72, -9.14, tc.day); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if *hits != 1 {
		t.Errorf("holiday list fetched %d times, want once for the year", *hits)
	}
}

func TestHolidayCalendarKnowsNothingWithoutACountry(t *testing.T) {
	source, hits := nagerSource(t, nagerFixture, http.StatusOK)
	ctx := context.Background()

	for name, country := range map[string]CountryResolver{
		"at sea":        fixedCountry{},
		"geocoder down": fixedCountry{err: errors.New("down")},
	} {
		if got := NewHolidayCalendar(country, source).Holiday(ctx, 0, 0, sept(2)); got != "" {
			t.Errorf("%s: got %q, want no holiday", name, got)
		}
	}
	if *hits != 0 {
		t.Errorf("holiday list fetched %d times, want none", *hits)
	}

	var none *HolidayCalendar
	if got := none.Holiday(ctx, 0, 0, sept(2)); got != "" {
		t.Errorf("nil calendar: got %q, want no holiday", got)
	}
	if got := NewHolidayCalendar(nil, nil).Holiday(ctx, 0, 0, sept(2)); got != "" {
		t.Errorf("empty calendar: got %q, want no holiday", got)
	}
}

func TestHolidayCalendarListUnavailable(t *testing.T) {
	source, _ := nagerSource(t, `nope`, http.StatusInternalServerError)
	cal := NewHolidayCalendar(fixedCountry{code: "PT"}, source)
	if got := cal.Holiday(context.Background(), 38.72, -9.14, sept(2)); got != "" {
		t.Errorf("got %q, want no holiday when the list cannot be read", got)
	}
}
