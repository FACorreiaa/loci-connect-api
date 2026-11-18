package localcontext

import (
	"context"
	"time"
)

// HolidayCalendar answers one question: is this day a public holiday at this
// place? It joins the two things that already exist here, a geocoder that
// knows the country and the holiday list for that country, for callers that
// have a coordinate and a date rather than a signal request.
type HolidayCalendar struct {
	country  CountryResolver
	holidays *HolidaySource
}

// NewHolidayCalendar builds the calendar. Either argument may be nil; the
// calendar then knows of no holidays.
func NewHolidayCalendar(country CountryResolver, holidays *HolidaySource) *HolidayCalendar {
	return &HolidayCalendar{country: country, holidays: holidays}
}

// Holiday returns the local name of the public holiday on day at the given
// place, or "" when the day is an ordinary one.
//
// Only nationwide public holidays count. A regional holiday may not apply
// where the traveller is, and only the country is known here; a bank or
// school day does not shut a museum. Saying "holiday" for either would put a
// warning on a day that needs none.
//
// A lookup that fails also answers "": the caller is deciding what to show
// next to opening hours, and an unreachable calendar is no reason to show
// nothing at all.
func (c *HolidayCalendar) Holiday(ctx context.Context, lat, lon float64, day time.Time) string {
	if c == nil || c.country == nil || c.holidays == nil {
		return ""
	}
	code, err := c.country.CountryCode(ctx, lat, lon)
	if err != nil || code == "" {
		return ""
	}
	list, err := c.holidays.holidaysFor(ctx, code, day.Year())
	if err != nil {
		return ""
	}
	want := day.Format("2006-01-02")
	for _, h := range list {
		if h.Date != want || !h.Global || !h.hasType("Public") {
			continue
		}
		if h.LocalName != "" {
			return h.LocalName
		}
		return h.Name
	}
	return ""
}
