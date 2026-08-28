package localcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FACorreiaa/loci-connect-api/pkg/httpx"
)

const nagerDateBaseURL = "https://date.nager.at"

// HolidaySource fills AlertKindHoliday from Nager.Date, a keyless public
// holiday API.
//
// This is the first real producer of Alerts. Before it, AlertHoliday existed in
// Go, in the proto and in the client — and LocalWeather already rendered an
// alert list — but nothing ever populated any of it, so the alert path and the
// scorer's disruption penalty had never executed.
//
// A public holiday is genuinely useful trip context and genuinely ambiguous
// news: museums and shops shut, but the city is often at its best. So it is
// scored as a moderate penalty, not a disqualifying one.
type HolidaySource struct {
	baseURL string
	client  *httpx.Client

	// Cached per country-year for a month. A year's public holidays are decided
	// long in advance and effectively never change, so this is the one source
	// where a long TTL is not a trade-off.
	cache *signalCache
	now   func() time.Time
}

// NewHolidaySource builds the source. An empty baseURL uses the public endpoint.
func NewHolidaySource(baseURL string, client *httpx.Client, cache *signalCache) *HolidaySource {
	if baseURL == "" {
		baseURL = nagerDateBaseURL
	}
	return &HolidaySource{baseURL: baseURL, client: client, cache: cache, now: time.Now}
}

func (s *HolidaySource) Name() string { return SourceHolidays }

type nagerHoliday struct {
	Date      string   `json:"date"`
	LocalName string   `json:"localName"`
	Name      string   `json:"name"`
	Global    bool     `json:"global"`
	Counties  []string `json:"counties"`
	Types     []string `json:"types"`
}

func (s *HolidaySource) Fetch(ctx context.Context, req SignalRequest) ([]Alert, error) {
	if req.CountryCode == "" {
		// Not an error: the coordinate is at sea, or the geocoder was
		// unavailable. Country-scoped sources stay quiet rather than guess.
		return nil, nil
	}

	start, end := req.Start, req.End
	if start.IsZero() {
		start = s.now().UTC()
	}
	if end.IsZero() || end.Before(start) {
		// Match the scorer's default assumption of a weekend.
		end = start.Add(48 * time.Hour)
	}

	// A window can straddle new year, so fetch every year it touches.
	years := yearsSpanned(start, end)

	var out []Alert
	for _, year := range years {
		holidays, err := s.holidaysFor(ctx, req.CountryCode, year)
		if err != nil {
			return nil, err
		}
		for _, h := range holidays {
			date, perr := time.Parse("2006-01-02", h.Date)
			if perr != nil {
				continue // one malformed row must not lose the rest
			}
			if !withinWindow(date, start, end) {
				continue
			}
			out = append(out, h.toAlert(date, req.CountryCode))
		}
	}
	return out, nil
}

func (s *HolidaySource) holidaysFor(ctx context.Context, country string, year int) ([]nagerHoliday, error) {
	key := fmt.Sprintf("%s:%d", country, year)

	if cached, ok := cacheGet[[]nagerHoliday](s.cache, SourceHolidays, key); ok {
		return cached, nil
	}

	endpoint := fmt.Sprintf("%s/api/v3/PublicHolidays/%d/%s", s.baseURL, year, country)
	holidays, err := httpx.GetJSON[[]nagerHoliday](ctx, s.client, SourceHolidays, endpoint)
	if err != nil {
		return nil, err
	}

	cacheSet(s.cache, SourceHolidays, key, holidays, ttlHolidays)
	return holidays, nil
}

// toAlert converts one Nager.Date row, grading it by how much it actually
// closes things.
func (h nagerHoliday) toAlert(date time.Time, country string) Alert {
	d := date
	title := h.LocalName
	if title == "" {
		title = h.Name
	}
	// Nager gives both the local and English name; showing both saves a
	// traveller having to recognise "Restauração da Independência".
	if h.Name != "" && !strings.EqualFold(h.Name, title) {
		title = fmt.Sprintf("%s (%s)", title, h.Name)
	}

	detail := fmt.Sprintf("Public holiday in %s — expect reduced opening hours and transport", country)
	severity := SeverityModerate

	// "Optional", "Bank", "School" and "Authorities" days do not shut a city
	// the way a public holiday does; treating them the same would penalise a
	// destination for a day most visitors would never notice.
	if !h.hasType("Public") {
		severity = SeverityMinor
		detail = fmt.Sprintf("Observance in %s — most places stay open", country)
	}
	// A holiday observed in only some regions may not apply where the traveller
	// actually is, and we only know the country, not the county.
	if !h.Global {
		severity = SeverityMinor
		regions := "some regions"
		if len(h.Counties) > 0 {
			regions = strings.Join(h.Counties, ", ")
		}
		detail = fmt.Sprintf("Regional holiday (%s) — may not affect where you are staying", regions)
	}

	return Alert{
		Kind:     AlertHoliday,
		Title:    title,
		Detail:   detail,
		Date:     &d,
		Severity: severity,
		Source:   SourceHolidays,
	}
}

func (h nagerHoliday) hasType(t string) bool {
	for _, got := range h.Types {
		if strings.EqualFold(got, t) {
			return true
		}
	}
	return false
}

// yearsSpanned lists every calendar year a window touches, so a New Year trip
// does not miss January's holidays.
func yearsSpanned(start, end time.Time) []int {
	first, last := start.UTC().Year(), end.UTC().Year()
	if last < first {
		last = first
	}
	// Guard against an absurd window producing thousands of requests.
	if last-first > 1 {
		last = first + 1
	}
	years := make([]int, 0, last-first+1)
	for y := first; y <= last; y++ {
		years = append(years, y)
	}
	return years
}
